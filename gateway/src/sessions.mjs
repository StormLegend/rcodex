import { CodexAppServer } from "./codex-app-server.mjs";
import {
  approvalFamily,
  isApprovalRequestMethod,
  parsePermissionMode,
  permissionSettingsForMode,
} from "./permissions.mjs";
import { createRequestStore } from "./requests.mjs";
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

function normalizeQuestions(raw) {
  if (!Array.isArray(raw)) return [];
  return raw.map((item) => ({
    id: String(item?.id ?? ""),
    header: String(item?.header ?? ""),
    question: String(item?.question ?? ""),
    isOther: Boolean(item?.isOther),
    isSecret: Boolean(item?.isSecret),
    options: Array.isArray(item?.options)
      ? item.options.map((option) => ({ label: String(option?.label ?? ""), description: String(option?.description ?? "") }))
      : [],
  }));
}

export function createSessionManager({
  config,
  store,
  bus,
  logger,
  appServerFactory,
  requestTimeoutMs = 600000,
}) {
  const factory = appServerFactory ?? (() => defaultAppServerFactory(config, logger));
  const requests = createRequestStore({ timeoutMs: requestTimeoutMs, logger });
  const threadToSession = new Map();
  let appServer;
  let starting;

  async function ensureAppServer() {
    if (appServer) return appServer;
    if (!starting) {
      starting = (async () => {
        const client = factory();
        client.onNotification(handleNotification);
        client.onServerRequest(handleServerRequest);
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
      payload: { sessionId, stream: "event", format: "jsonl", eventType: method, jsonPayload: params ?? {} },
    });
  }

  function setStatus(sessionId, status, extra = {}) {
    const updated = store.update(sessionId, () => ({ status, lastUpdatedAt: nowIso(), ...extra }));
    if (updated) bus.emit(sessionId, { type: "session-status", payload: { sessionId, status, session: updated } });
    return updated;
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
        setStatus(sessionId, nextStatus, TERMINAL_STATUSES.has(nextStatus) ? { finishedAt: nowIso(), lastTurnFinishedAt: nowIso() } : {});
      }
      return;
    }

    if (message.method === "turn/completed") {
      setStatus(sessionId, "completed", {
        activeTurnId: undefined,
        finishedAt: nowIso(),
        lastTurnFinishedAt: nowIso(),
      });
    }
  }

  /**
   * Server→client requests from the app-server. Returns true when we took
   * ownership of the request (the client responds itself).
   */
  async function handleServerRequest(message) {
    const params = message.params ?? {};
    const threadId = params.threadId ?? params.thread?.id;
    const sessionId = threadId ? threadToSession.get(threadId) : undefined;
    const session = sessionId ? store.get(sessionId) : undefined;
    if (!session) {
      logger.warn(`refusing request for an unknown thread: method=${message.method} threadId=${threadId ?? "unknown"}`);
      return false;
    }
    const mode = session.permissionMode ?? "full";

    if (isApprovalRequestMethod(message.method)) {
      const payload = {
        family: approvalFamily(message.method),
        requestMethod: message.method,
        reason: params.reason ?? params.message ?? "Codex 请求授权",
        command: params.command,
        cwd: params.cwd,
        itemId: params.itemId,
      };

      if (mode === "ask") {
        const entry = requests.create({
          sessionId,
          kind: "approval",
          summary: payload.reason,
          payload,
          backendRequestId: message.id,
        });
        setStatus(sessionId, "waiting-approval", { activeTurnId: session.activeTurnId });
        bus.emit(sessionId, {
          type: "session-approval",
          payload: { sessionId, request: { id: entry.id, kind: "approval", payload, createdAt: entry.createdAt } },
        });
        const decision = await entry.promise;
        const approved = decision?.decision === "approve";
        appServer.respond(message.id, { decision: approved ? "accept" : "decline" });
        bus.emit(sessionId, {
          type: "session-approval-resolved",
          payload: { sessionId, id: entry.id, decision: approved ? "approved" : "rejected", note: decision?.note },
        });
        setStatus(sessionId, "running");
        return true;
      }

      // auto: Codex reviews the escalation itself; full: nothing is restricted.
      appServer.respond(message.id, { decision: "accept" });
      bus.emit(sessionId, {
        type: "session-approval-auto",
        payload: { sessionId, mode, requestMethod: message.method, reason: payload.reason },
      });
      return true;
    }

    if (message.method === "item/tool/requestUserInput") {
      const questions = normalizeQuestions(params.questions);
      const entry = requests.create({
        sessionId,
        kind: "question",
        summary: questions.map((question) => question.question).filter(Boolean).join(" / ") || "需要用户回答",
        payload: { questions },
        backendRequestId: message.id,
      });
      setStatus(sessionId, "waiting-approval", { activeTurnId: session.activeTurnId });
      bus.emit(sessionId, {
        type: "session-question",
        payload: { sessionId, request: { id: entry.id, kind: "question", questions, createdAt: entry.createdAt } },
      });
      const answer = await entry.promise;
      appServer.respond(message.id, { answers: answer?.answers ?? {} });
      bus.emit(sessionId, {
        type: "session-question-answered",
        payload: { sessionId, id: entry.id, answers: answer?.answers ?? {} },
      });
      setStatus(sessionId, "running");
      return true;
    }

    return false;
  }

  async function createSession({ workspacePath, prompt, model, reasoningEffort, title, permissionMode }) {
    const cwd = trimmed(workspacePath) || config.allowedPaths[0];
    const cleanPrompt = trimmed(prompt);
    if (!cleanPrompt) {
      const error = new Error("prompt is required");
      error.statusCode = 400;
      throw error;
    }
    const mode = parsePermissionMode(permissionMode, config.defaultPermissionMode ?? "full");
    const settings = permissionSettingsForMode(mode, cwd);
    const client = await ensureAppServer();
    const sessionId = newId();
    const createdAt = nowIso();
    const requestedModel = trimmed(model) || config.codexModels[0] || undefined;

    const thread = await client.startThread({
      cwd,
      model: requestedModel,
      approvalPolicy: settings.approvalPolicy,
      approvalsReviewer: settings.approvalsReviewer,
      sandbox: settings.sandbox,
    });
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
      permissionMode: mode,
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
        approvalPolicy: settings.approvalPolicy,
        approvalsReviewer: settings.approvalsReviewer,
        sandboxPolicy: settings.sandboxPolicy,
      });
      setStatus(sessionId, "running", { activeTurnId: turnId, lastTurnStartedAt: nowIso() });
    } catch (error) {
      setStatus(sessionId, "failed", { exitReason: String(error.message ?? error), finishedAt: nowIso() });
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
    const settings = permissionSettingsForMode(session.permissionMode ?? "full", session.workspacePath);
    const client = await ensureAppServer();
    const turnId = await client.startTurn({
      threadId: session.providerSessionId,
      prompt: cleanPrompt,
      cwd: session.workspacePath,
      model: session.modelOverride,
      effort: session.reasoningEffort,
      approvalPolicy: settings.approvalPolicy,
      approvalsReviewer: settings.approvalsReviewer,
      sandboxPolicy: settings.sandboxPolicy,
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

  function listRequests(sessionId) {
    return requests.list(sessionId);
  }

  async function resolveApproval(sessionId, requestId, decision, note) {
    const entry = requests.get(requestId);
    if (!entry || entry.sessionId !== sessionId || entry.kind !== "approval") {
      const error = new Error("approval request not found");
      error.statusCode = 404;
      throw error;
    }
    await requests.resolve(requestId, { decision, note });
    return { id: requestId, decision };
  }

  async function answerQuestion(sessionId, requestId, answers) {
    const entry = requests.get(requestId);
    if (!entry || entry.sessionId !== sessionId || entry.kind !== "question") {
      const error = new Error("question request not found");
      error.statusCode = 404;
      throw error;
    }
    await requests.resolve(requestId, { answers });
    return { id: requestId, answers };
  }

  function remove(sessionId) {
    const session = store.get(sessionId);
    if (!session) return false;
    requests.cancelForSession(sessionId);
    if (session.providerSessionId) threadToSession.delete(session.providerSessionId);
    store.remove(sessionId);
    bus.forget(sessionId);
    return true;
  }

  async function shutdown() {
    await appServer?.stop();
    appServer = undefined;
  }

  return {
    createSession,
    runTurn,
    interrupt,
    remove,
    shutdown,
    ensureAppServer,
    listRequests,
    resolveApproval,
    answerQuestion,
  };
}
