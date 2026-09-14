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

function normalizeModelProvider(value) {
  const provider = trimmed(value);
  if (!provider) return undefined;
  if (!/^[A-Za-z0-9_-]+$/.test(provider)) {
    const error = new Error("modelProvider 只能包含字母、数字、下划线和连字符");
    error.statusCode = 400;
    error.code = "invalid_model_provider";
    throw error;
  }
  return provider;
}

export function createSessionManager({
  config,
  store,
  bus,
  logger,
  attachmentStore,
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

    if (message.method === "thread/tokenUsage/updated") {
      const tokenUsage = params.tokenUsage ?? params.usage;
      const updated = store.update(sessionId, () => ({
        usage: tokenUsage && typeof tokenUsage === "object" ? tokenUsage : undefined,
        lastUpdatedAt: nowIso(),
      }));
      if (updated) {
        bus.emit(sessionId, { type: "session-usage", payload: { sessionId, usage: updated.usage, session: updated } });
      }
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

  async function createSession({ workspacePath, prompt, model, modelProvider, reasoningEffort, title, permissionMode }) {
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
    const requestedProvider = normalizeModelProvider(modelProvider) || config.codexDefaultProvider || "custom";
    const providerEntry = (config.codexProviders ?? []).find((entry) => entry.provider === requestedProvider);
    if ((config.codexProviders ?? []).length > 0 && !providerEntry) {
      const error = new Error(`unknown model provider: ${requestedProvider}`);
      error.statusCode = 400;
      error.code = "unknown_model_provider";
      throw error;
    }
    const requestedModel = trimmed(model) || providerEntry?.models?.[0] || config.codexModels[0] || undefined;

    const thread = await client.startThread({
      cwd,
      model: requestedModel,
      modelProvider: requestedProvider,
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
      modelProvider: thread.modelProvider ?? requestedProvider,
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
      attachments: [],
      usage: undefined,
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
      const current = store.get(sessionId);
      if (current?.status === "waiting-approval") {
        store.update(sessionId, () => ({ activeTurnId: turnId, lastTurnStartedAt: nowIso() }));
      } else {
        setStatus(sessionId, "running", { activeTurnId: turnId, lastTurnStartedAt: nowIso() });
      }
    } catch (error) {
      setStatus(sessionId, "failed", { exitReason: String(error.message ?? error), finishedAt: nowIso() });
      throw error;
    }

    return store.get(sessionId);
  }

  async function runTurn(sessionId, prompt, attachmentIds = []) {
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
    const requestedAttachmentIds = [...new Set(attachmentIds.map(String))];
    const selectedAttachments = (session.attachments ?? []).filter((attachment) => requestedAttachmentIds.includes(attachment.id));
    if (selectedAttachments.length !== requestedAttachmentIds.length) {
      const error = new Error("one or more attachments do not belong to this session");
      error.statusCode = 400;
      error.code = "invalid_attachment_reference";
      throw error;
    }
    const attachments = attachmentStore
      ? await Promise.all(selectedAttachments.map((attachment) => attachmentStore.promptAttachment(session, attachment)))
      : [];
    const turnId = await client.startTurn({
      threadId: session.providerSessionId,
      prompt: cleanPrompt,
      cwd: session.workspacePath,
      model: session.modelOverride,
      effort: session.reasoningEffort,
      approvalPolicy: settings.approvalPolicy,
      approvalsReviewer: settings.approvalsReviewer,
      sandboxPolicy: settings.sandboxPolicy,
      attachments,
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

  async function addAttachment(sessionId, input) {
    const session = store.get(sessionId);
    if (!session) {
      const error = new Error("session not found");
      error.statusCode = 404;
      throw error;
    }
    if (!attachmentStore) throw new Error("attachments are not configured");
    if ((session.attachments ?? []).length >= 20) {
      const error = new Error("session attachment limit exceeded");
      error.statusCode = 400;
      error.code = "too_many_attachments";
      throw error;
    }
    const attachment = await attachmentStore.store(session, input);
    const updated = store.update(sessionId, (current) => ({
      attachments: [...(current.attachments ?? []), attachment],
      lastUpdatedAt: nowIso(),
    }));
    return updated?.attachments?.find((item) => item.id === attachment.id) ?? attachment;
  }

  async function updateRuntimeConfig(sessionId, input = {}) {
    const session = store.get(sessionId);
    if (!session) {
      const error = new Error("session not found");
      error.statusCode = 404;
      throw error;
    }
    if (input.modelProvider !== undefined && normalizeModelProvider(input.modelProvider) !== session.modelProvider) {
      const error = new Error("Provider 绑定在 Codex thread 上，不能在原会话中切换；请新建会话");
      error.statusCode = 409;
      error.code = "model_provider_immutable";
      throw error;
    }
    const model = input.model === undefined ? session.modelOverride : trimmed(input.model) || undefined;
    const reasoningEffort = input.reasoningEffort === undefined ? session.reasoningEffort : trimmed(input.reasoningEffort) || undefined;
    const client = await ensureAppServer();
    await client.updateThreadSettings(session.providerSessionId, {
      model,
      reasoningEffort,
      serviceTier: input.serviceTier,
    });
    const updated = store.update(sessionId, () => ({
      modelOverride: model,
      modelLabel: model,
      reasoningEffort,
      lastUpdatedAt: nowIso(),
    }));
    bus.emit(sessionId, { type: "session-runtime-config", payload: { sessionId, session: updated } });
    return updated;
  }

  async function resumeSession(sessionId) {
    const session = store.get(sessionId);
    if (!session) {
      const error = new Error("session not found");
      error.statusCode = 404;
      throw error;
    }
    if (["completed", "failed"].includes(session.status)) return session;
    const settings = permissionSettingsForMode(session.permissionMode ?? "full", session.workspacePath);
    const client = await ensureAppServer();
    try {
      const thread = await client.resumeThread({
        threadId: session.providerSessionId,
        cwd: session.workspacePath,
        model: session.modelOverride,
        modelProvider: session.modelProvider,
        approvalPolicy: settings.approvalPolicy,
        approvalsReviewer: settings.approvalsReviewer,
        sandbox: settings.sandbox,
      });
      const threadId = thread?.id ?? session.providerSessionId;
      threadToSession.set(threadId, sessionId);
      const updated = store.update(sessionId, () => ({
        providerSessionId: threadId,
        status: "running",
        resumeStatus: "resumed",
        lastUpdatedAt: nowIso(),
      }));
      bus.emit(sessionId, { type: "session-resumed", payload: { sessionId, session: updated } });
      return updated;
    } catch (error) {
      const updated = store.update(sessionId, () => ({
        status: "failed",
        resumeStatus: "failed",
        exitReason: `resume failed: ${error.message ?? String(error)}`,
        lastUpdatedAt: nowIso(),
      }));
      bus.emit(sessionId, { type: "session-resume-failed", payload: { sessionId, session: updated } });
      throw error;
    }
  }

  async function resumePersistedSessions() {
    const resumable = store.list().filter((session) => !["completed", "failed"].includes(session.status) && session.providerSessionId);
    const results = [];
    for (const session of resumable) {
      try {
        results.push(await resumeSession(session.id));
      } catch (error) {
        logger.warn(`could not resume session ${session.id}: ${error.message ?? String(error)}`);
      }
    }
    return results;
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
    addAttachment,
    updateRuntimeConfig,
    resumeSession,
    resumePersistedSessions,
  };
}
