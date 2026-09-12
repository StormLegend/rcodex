import { CodexAppServer } from "./codex-app-server.mjs";
import { newId, nowIso, trimmed } from "./util.mjs";

const TERMINAL_STATUSES = new Set(["completed", "failed"]);

function defaultAppServerFactory(config, logger) {
  return new CodexAppServer({
    command: config.codexCommand,
    args: ["app-server"],
    cwd: config.allowedPaths[0],
    logger,
    requestTimeoutMs: config.codexAppServerStartupTimeoutMs,
  });
}

export function createSessionManager({ config, store, bus, logger, appServerFactory }) {
  const factory = appServerFactory ?? (() => defaultAppServerFactory(config, logger));
  const threadToSession = new Map();
  let appServer;
  let starting;

  async function ensureAppServer() {
    if (appServer) return appServer;
    if (!starting) {
      starting = (async () => {
        const client = factory();
        client.onNotification(handleNotification);
        client.onServerRequest((message) => {
          logger.warn("codex app-server requested", message.method, "- refusing (no approval UI yet)");
          return false;
        });
        await client.initialize();
        appServer = client;
        starting = undefined;
        return client;
      })().catch((error) => {
        starting = undefined;
        throw error;
      });
    }
    return starting;
  }

  function emitOutput(sessionId, method, params) {
    bus.emit(sessionId, {
      type: "session-output",
      payload: {
        sessionId,
        stream: "event",
        format: "jsonl",
        eventType: method,
        jsonPayload: params ?? {},
      },
    });
  }

  function handleNotification(message) {
    const params = message.params ?? {};
    const threadId = params.threadId ?? params.thread?.id;
    const sessionId = threadId ? threadToSession.get(threadId) : undefined;
    if (!sessionId) return;
    const session = store.get(sessionId);
    if (!session) return;

    emitOutput(sessionId, message.method, params);

    if (message.method === "item/agentMessage/delta") {
      bus.emit(sessionId, {
        type: "session-message-delta",
        payload: { sessionId, text: params.delta ?? params.text ?? "" },
      });
      return;
    }

    if (message.method === "thread/status/changed") {
      const status = params.status?.type ?? params.status;
      const nextStatus =
        status === "systemError" ? "failed" : status === "idle" ? "completed" : status === "active" ? "running" : undefined;
      if (nextStatus && nextStatus !== session.status) {
        const updated = store.update(sessionId, () => ({
          status: nextStatus,
          lastUpdatedAt: nowIso(),
          ...(TERMINAL_STATUSES.has(nextStatus) ? { finishedAt: nowIso(), lastTurnFinishedAt: nowIso() } : {}),
        }));
        bus.emit(sessionId, { type: "session-status", payload: { sessionId, status: nextStatus, session: updated } });
      }
      return;
    }

    if (message.method === "turn/completed") {
      const updated = store.update(sessionId, () => ({
        status: "completed",
        activeTurnId: undefined,
        finishedAt: nowIso(),
        lastTurnFinishedAt: nowIso(),
        lastUpdatedAt: nowIso(),
      }));
      bus.emit(sessionId, { type: "session-status", payload: { sessionId, status: "completed", session: updated } });
    }
  }

  async function createSession({ workspacePath, prompt, model, reasoningEffort, title, permissionMode }) {
    const cwd = trimmed(workspacePath) || config.allowedPaths[0];
    const cleanPrompt = trimmed(prompt);
    if (!cleanPrompt) {
      const error = new Error("prompt is required");
      error.statusCode = 400;
      throw error;
    }
    const client = await ensureAppServer();
    const sessionId = newId();
    const createdAt = nowIso();
    const requestedModel = trimmed(model) || config.codexModels[0] || undefined;

    let thread;
    try {
      thread = await client.startThread({
        cwd,
        model: requestedModel,
        approvalPolicy: permissionMode === "full" || !permissionMode ? "never" : "on-request",
        approvalsReviewer: "user",
        sandbox: "danger-full-access",
      });
    } catch (error) {
      error.statusCode = error.statusCode ?? 502;
      throw error;
    }
    const threadId = thread?.id;
    if (!threadId) {
      const error = new Error("codex app-server did not return a thread id");
      error.statusCode = 502;
      throw error;
    }
    threadToSession.set(threadId, sessionId);

    const session = {
      id: sessionId,
      sessionType: "development",
      provider: "codex",
      providerSessionId: threadId,
      title: trimmed(title) || cleanPrompt.slice(0, 40),
      latestPrompt: cleanPrompt,
      workspacePath: cwd,
      status: "starting",
      canResume: true,
      resumeStatus: "resumable",
      command: `${config.codexCommand} app-server`,
      args: ["thread/start", "turn/start"],
      prompt: cleanPrompt,
      modelOverride: requestedModel,
      modelLabel: thread.model ?? requestedModel,
      reasoningEffort: reasoningEffort ?? thread.reasoningEffort,
      createdAt,
      startedAt: createdAt,
      lastUpdatedAt: createdAt,
      lastTurnStartedAt: createdAt,
    };
    store.upsert(session);
    bus.emit(sessionId, { type: "session-started", payload: { session } });

    try {
      const turnId = await client.startTurn({
        threadId,
        prompt: cleanPrompt,
        cwd,
        model: requestedModel,
        effort: reasoningEffort,
        approvalPolicy: "never",
        approvalsReviewer: "user",
        sandboxPolicy: { type: "dangerFullAccess" },
      });
      const running = store.update(sessionId, () => ({
        status: "running",
        activeTurnId: turnId,
        lastUpdatedAt: nowIso(),
      }));
      bus.emit(sessionId, { type: "session-status", payload: { sessionId, status: "running", session: running } });
    } catch (error) {
      const failed = store.update(sessionId, () => ({
        status: "failed",
        exitReason: String(error.message ?? error),
        finishedAt: nowIso(),
        lastUpdatedAt: nowIso(),
      }));
      bus.emit(sessionId, { type: "session-status", payload: { sessionId, status: "failed", session: failed } });
      throw error;
    }

    return store.get(sessionId);
  }

  async function runTurn(sessionId, prompt) {
    const session = store.get(sessionId);
    if (!session) {
      const error = new Error("session not found");
      error.statusCode = 404;
      throw error;
    }
    const cleanPrompt = trimmed(prompt);
    if (!cleanPrompt) {
      const error = new Error("prompt is required");
      error.statusCode = 400;
      throw error;
    }
    const client = await ensureAppServer();
    const turnId = await client.startTurn({
      threadId: session.providerSessionId,
      prompt: cleanPrompt,
      cwd: session.workspacePath,
      model: session.modelOverride,
      effort: session.reasoningEffort,
      approvalPolicy: "never",
      approvalsReviewer: "user",
      sandboxPolicy: { type: "dangerFullAccess" },
    });
    const updated = store.update(sessionId, () => ({
      status: "running",
      activeTurnId: turnId,
      latestPrompt: cleanPrompt,
      lastUpdatedAt: nowIso(),
      lastTurnStartedAt: nowIso(),
      finishedAt: undefined,
    }));
    bus.emit(sessionId, { type: "session-status", payload: { sessionId, status: "running", session: updated } });
    return updated;
  }

  async function interrupt(sessionId) {
    const session = store.get(sessionId);
    if (!session) {
      const error = new Error("session not found");
      error.statusCode = 404;
      throw error;
    }
    const client = await ensureAppServer();
    await client.interrupt(session.providerSessionId);
    return store.get(sessionId);
  }

  function remove(sessionId) {
    const session = store.get(sessionId);
    if (!session) return false;
    if (session.providerSessionId) threadToSession.delete(session.providerSessionId);
    store.remove(sessionId);
    bus.forget(sessionId);
    return true;
  }

  async function shutdown() {
    await appServer?.stop();
    appServer = undefined;
  }

  return { createSession, runTurn, interrupt, remove, shutdown, ensureAppServer };
}
