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

function timestampFromThread(value, fallback = nowIso()) {
  if (typeof value === "number" && Number.isFinite(value)) return new Date(value < 10_000_000_000 ? value * 1000 : value).toISOString();
  const parsed = Date.parse(String(value ?? ""));
  return Number.isFinite(parsed) ? new Date(parsed).toISOString() : fallback;
}

function itemText(item) {
  if (typeof item?.text === "string") return item.text;
  if (Array.isArray(item?.content)) return item.content.map((part) => part?.text ?? part?.input_text ?? part?.output_text ?? "").join("");
  return "";
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

  async function forkSession(sessionId, input = {}) {
    const source = store.get(sessionId);
    if (!source) {
      const error = new Error("session not found");
      error.statusCode = 404;
      throw error;
    }
    const requestedProvider = input.modelProvider === undefined
      ? source.modelProvider
      : normalizeModelProvider(input.modelProvider);
    const providerEntry = (config.codexProviders ?? []).find((entry) => entry.provider === requestedProvider);
    if ((config.codexProviders ?? []).length > 0 && !providerEntry) {
      const error = new Error(`unknown model provider: ${requestedProvider}`);
      error.statusCode = 400;
      error.code = "unknown_model_provider";
      throw error;
    }
    const mode = parsePermissionMode(input.permissionMode, source.permissionMode ?? config.defaultPermissionMode ?? "full");
    const settings = permissionSettingsForMode(mode, source.workspacePath);
    const model = trimmed(input.model) || source.modelOverride || providerEntry?.models?.[0];
    const client = await ensureAppServer();
    const thread = await client.forkThread(source.providerSessionId, {
      cwd: source.workspacePath,
      model,
      modelProvider: requestedProvider,
      approvalPolicy: settings.approvalPolicy,
      approvalsReviewer: settings.approvalsReviewer,
      sandbox: settings.sandbox,
    });
    const threadId = thread?.id;
    if (!threadId) {
      const error = new Error("codex app-server did not return a forked thread id");
      error.statusCode = 502;
      throw error;
    }
    const forkedId = newId();
    const createdAt = nowIso();
    const prompt = trimmed(input.prompt);
    const session = {
      id: forkedId,
      sessionType: "development",
      provider: "codex",
      modelProvider: thread.modelProvider ?? requestedProvider,
      providerSessionId: threadId,
      forkedFromSessionId: source.id,
      title: trimmed(input.title) || `${source.title}（分支）`,
      latestPrompt: prompt || source.latestPrompt,
      workspacePath: source.workspacePath,
      status: prompt ? "starting" : "completed",
      permissionMode: mode,
      canResume: true,
      resumeStatus: "resumable",
      command: `${config.codexCommand} app-server`,
      args: ["thread/fork", "turn/start"],
      modelOverride: model,
      modelLabel: thread.model ?? model,
      reasoningEffort: input.reasoningEffort ?? source.reasoningEffort ?? thread.reasoningEffort,
      createdAt,
      startedAt: createdAt,
      lastUpdatedAt: createdAt,
      attachments: [],
      usage: undefined,
    };
    threadToSession.set(threadId, forkedId);
    store.upsert(session);
    bus.emit(forkedId, { type: "session-started", payload: { session } });
    if (!prompt) return session;
    try {
      const turnId = await client.startTurn({
        threadId,
        prompt,
        cwd: source.workspacePath,
        model,
        effort: session.reasoningEffort,
        approvalPolicy: settings.approvalPolicy,
        approvalsReviewer: settings.approvalsReviewer,
        sandboxPolicy: settings.sandboxPolicy,
      });
      setStatus(forkedId, "running", { activeTurnId: turnId, lastTurnStartedAt: nowIso() });
    } catch (error) {
      setStatus(forkedId, "failed", { exitReason: String(error.message ?? error), finishedAt: nowIso() });
      throw error;
    }
    return store.get(forkedId);
  }

  async function steerSession(sessionId, prompt, attachmentIds = []) {
    const session = store.get(sessionId);
    if (!session) {
      const error = new Error("session not found");
      error.statusCode = 404;
      throw error;
    }
    if (!session.activeTurnId || !["running", "waiting-approval"].includes(session.status)) {
      const error = new Error("session has no active turn to steer");
      error.statusCode = 409;
      error.code = "no_active_turn";
      throw error;
    }
    const cleanPrompt = trimmed(prompt);
    if (!cleanPrompt) {
      const error = new Error("prompt is required");
      error.statusCode = 400;
      throw error;
    }
    const selected = (session.attachments ?? []).filter((item) => attachmentIds.map(String).includes(item.id));
    if (selected.length !== new Set(attachmentIds.map(String)).size) {
      const error = new Error("one or more attachments do not belong to this session");
      error.statusCode = 400;
      error.code = "invalid_attachment_reference";
      throw error;
    }
    const attachments = attachmentStore
      ? await Promise.all(selected.map((item) => attachmentStore.promptAttachment(session, item)))
      : [];
    const client = await ensureAppServer();
    const turnId = await client.steerTurn(session.providerSessionId, session.activeTurnId, cleanPrompt, attachments);
    const updated = store.update(sessionId, () => ({ latestPrompt: cleanPrompt, lastUpdatedAt: nowIso(), activeTurnId: turnId || session.activeTurnId }));
    bus.emit(sessionId, { type: "session-steered", payload: { sessionId, prompt: cleanPrompt, session: updated } });
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

  async function listImportableThreads(limit = 500) {
    const client = await ensureAppServer();
    const existing = new Set(store.list().map((session) => session.providerSessionId).filter(Boolean));
    const threads = await client.listThreads(Math.max(1, Math.min(1000, Number(limit) || 500)));
    return threads.filter((thread) => thread?.id && !existing.has(thread.id)).map((thread) => ({
      id: thread.id,
      preview: thread.preview ?? thread.title ?? "",
      cwd: thread.cwd ?? "",
      model: thread.model,
      modelProvider: thread.modelProvider,
      createdAt: timestampFromThread(thread.createdAt),
      updatedAt: timestampFromThread(thread.updatedAt ?? thread.createdAt),
    }));
  }

  async function importSession(threadId, title) {
    const normalizedThreadId = trimmed(threadId);
    if (!normalizedThreadId) {
      const error = new Error("threadId is required");
      error.statusCode = 400;
      throw error;
    }
    const existing = store.list().find((session) => session.providerSessionId === normalizedThreadId);
    if (existing) return existing;
    const client = await ensureAppServer();
    const [thread, items] = await Promise.all([
      client.readThread(normalizedThreadId),
      client.listThreadItems(normalizedThreadId).catch(() => []),
    ]);
    if (!thread?.id) {
      const error = new Error("thread not found");
      error.statusCode = 404;
      throw error;
    }
    const sessionId = newId();
    const createdAt = timestampFromThread(thread.createdAt);
    const updatedAt = timestampFromThread(thread.updatedAt ?? thread.createdAt, createdAt);
    let workspacePath = config.allowedPaths[0];
    let canResume = false;
    try {
      workspacePath = (await files.resolveAllowed(thread.cwd || config.allowedPaths[0])).path;
      canResume = true;
    } catch {
      // Keep imported history viewable, but never resume into an unapproved path.
    }
    const session = {
      id: sessionId,
      sessionType: "development",
      provider: "codex",
      modelProvider: thread.modelProvider,
      providerSessionId: thread.id,
      title: trimmed(title) || trimmed(thread.preview ?? thread.title) || "已导入会话",
      latestPrompt: trimmed(thread.preview),
      workspacePath,
      status: "completed",
      permissionMode: config.defaultPermissionMode ?? "full",
      canResume,
      resumeStatus: canResume ? "resumable" : "history-only",
      command: `${config.codexCommand} app-server`,
      args: ["thread/import", "history/replay"],
      modelOverride: thread.model,
      modelLabel: thread.model,
      reasoningEffort: thread.reasoningEffort,
      createdAt,
      startedAt: createdAt,
      lastUpdatedAt: updatedAt,
      finishedAt: updatedAt,
      lastTurnFinishedAt: updatedAt,
      attachments: [],
    };
    store.upsert(session);
    threadToSession.set(thread.id, sessionId);
    for (const item of items) {
      const text = itemText(item);
      const type = String(item?.type ?? "");
      const timestamp = timestampFromThread(item?.createdAt ?? item?.timestamp, updatedAt);
      if (/user/i.test(type) && text) bus.emit(sessionId, { type: "session-user-message", timestamp, payload: { sessionId, text } });
      else if (/agentMessage|assistant/i.test(type) && text) bus.emit(sessionId, { type: "session-message-delta", timestamp, payload: { sessionId, text } });
      bus.emit(sessionId, { type: "session-output", timestamp, payload: { sessionId, stream: "event", format: "jsonl", eventType: "item/completed", jsonPayload: { threadId: thread.id, item } } });
    }
    bus.emit(sessionId, { type: "session-imported", timestamp: updatedAt, payload: { sessionId, session } });
    return session;
  }

  async function readAttachment(sessionId, attachmentId) {
    const session = store.get(sessionId);
    if (!session) {
      const error = new Error("session not found");
      error.statusCode = 404;
      throw error;
    }
    const attachment = (session.attachments ?? []).find((item) => item.id === attachmentId);
    if (!attachment) {
      const error = new Error("attachment not found");
      error.statusCode = 404;
      throw error;
    }
    return attachmentStore.read(session, attachment);
  }

  async function listExtensions(kind, options = {}) {
    const client = await ensureAppServer();
    if (kind === "skills") return client.listSkills(options.cwd || config.allowedPaths[0], Boolean(options.forceReload));
    if (kind === "plugins") return client.listPlugins(options.cwd ? [options.cwd] : config.allowedPaths, Boolean(options.forceRefetch));
    if (kind === "mcp-servers") return client.listMcpServerStatuses(options.limit);
    if (kind === "apps") return client.listApps(Boolean(options.forceRefetch), options.limit);
    if (kind === "hooks") return client.listHooks(options.cwd || config.allowedPaths[0]);
    const error = new Error("unsupported extension kind");
    error.statusCode = 400;
    error.code = "unsupported_extension_kind";
    throw error;
  }

  async function refreshMcpServers() {
    const client = await ensureAppServer();
    await client.refreshMcpServers();
    return { refreshed: true };
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
    forkSession,
    steerSession,
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
    listImportableThreads,
    importSession,
    readAttachment,
    listExtensions,
    refreshMcpServers,
  };
}
