import http from "node:http";
import { createAttachmentStore } from "./attachments.mjs";
import { createChangesService } from "./changes.mjs";
import { createCaptchaStore, isAuthorized, validateCredentials } from "./auth.mjs";
import { renderConsoleHtml } from "./console.mjs";
import { createEventBus } from "./events.mjs";
import { createFilesystem } from "./filesystem.mjs";
import { createSessionStore } from "./session-store.mjs";
import { createSessionManager } from "./sessions.mjs";
import { createScheduleService } from "./schedules.mjs";
import { nowIso, readJsonBody, sendError, sendJson } from "./util.mjs";

function matchSessionChild(pathname, suffix) {
  const match = pathname.match(new RegExp(`^/sessions/([^/]+)${suffix}$`));
  return match ? decodeURIComponent(match[1]) : undefined;
}

function matchSessionAction(pathname, action) {
  const match = pathname.match(new RegExp(`^/sessions/([^/]+)/${action}/([^/]+)$`));
  return match ? { sessionId: decodeURIComponent(match[1]), requestId: decodeURIComponent(match[2]) } : undefined;
}

export function createGatewayServer({ config, logger }) {
  const captcha = createCaptchaStore();
  const bus = createEventBus({ dataDir: config.dataDir });
  const store = createSessionStore({ dataDir: config.dataDir });
  store.load();
  const files = createFilesystem({ allowedPaths: config.allowedPaths });
  const attachments = createAttachmentStore({ files });
  const changes = createChangesService({ files });
  const sessions = createSessionManager({ config, store, bus, logger, attachmentStore: attachments });
  const schedules = createScheduleService({ dataDir: config.dataDir, sessions, files, logger });

  async function handlePublic(req, res, pathname) {
    if (req.method === "GET" && (pathname === "/health" || pathname === "/healthz")) {
      const timestamp = nowIso();
      sendJson(res, 200, { status: "ok", ok: true, name: config.name, gatewayName: config.name, version: config.version, time: timestamp, timestamp });
      return true;
    }
    if (req.method === "GET" && (pathname === "/" || pathname === "")) {
      res.writeHead(302, { Location: "/console" });
      res.end();
      return true;
    }
    if (req.method === "GET" && pathname === "/console") {
      const body = renderConsoleHtml({ gatewayName: config.name, version: config.version });
      res.writeHead(200, { "Content-Type": "text/html; charset=utf-8", "Content-Length": Buffer.byteLength(body) });
      res.end(body);
      return true;
    }
    if (req.method === "GET" && pathname === "/auth/captcha") {
      const { captchaId, imageSvg, expiresInSeconds } = captcha.createChallenge();
      sendJson(res, 200, { captchaId, imageSvg, expiresInSeconds });
      return true;
    }
    if (req.method === "POST" && pathname === "/auth/login") {
      const body = await readJsonBody(req);
      if (!body.username || !body.password) {
        sendError(res, 400, "username and password are required", "invalid_request");
        return true;
      }
      if (!body.captchaId || !body.captchaAnswer) {
        sendError(res, 400, "captchaId and captchaAnswer are required", "captcha_required");
        return true;
      }
      if (!captcha.verify(body.captchaId, body.captchaAnswer)) {
        sendError(res, 401, "captcha is invalid or expired", "captcha_invalid");
        return true;
      }
      if (!validateCredentials(config, body.username, body.password)) {
        sendError(res, 401, "invalid username or password", "unauthorized");
        return true;
      }
      sendJson(res, 200, { token: config.authToken, username: config.authUsername, gatewayName: config.name });
      return true;
    }
    if (req.method === "GET" && pathname === "/version") {
      sendJson(res, 200, {
        name: config.name,
        version: config.version,
        models: config.codexModels,
        defaultModelProvider: config.codexDefaultProvider,
        providers: config.codexProviders ?? [],
      });
      return true;
    }
    return false;
  }

  async function handleSessions(req, res, pathname, searchParams) {
    if (req.method === "GET" && pathname === "/importable-threads") {
      sendJson(res, 200, { threads: await sessions.listImportableThreads(searchParams?.get("limit")) });
      return true;
    }
    if (req.method === "POST" && pathname === "/sessions/import") {
      const body = await readJsonBody(req);
      sendJson(res, 200, { session: await sessions.importSession(body.threadId, body.title) });
      return true;
    }
    if (req.method === "GET" && pathname === "/sessions") {
      sendJson(res, 200, { sessions: store.list() });
      return true;
    }
    if (req.method === "POST" && pathname === "/sessions") {
      const body = await readJsonBody(req);
      const session = await sessions.createSession({
        workspacePath: body.workspacePath,
        prompt: body.prompt,
        model: body.model,
        modelProvider: body.modelProvider,
        reasoningEffort: body.reasoningEffort,
        title: body.title,
        permissionMode: body.permissionMode,
      });
      sendJson(res, 201, { session });
      return true;
    }
    if (req.method === "GET" && pathname === "/schedules") {
      sendJson(res, 200, { schedules: schedules.list() });
      return true;
    }
    if (req.method === "POST" && pathname === "/schedules") {
      const body = await readJsonBody(req);
      sendJson(res, 201, { schedule: await schedules.create(body) });
      return true;
    }
    if (req.method === "GET" && pathname === "/schedule-runs") {
      sendJson(res, 200, { runs: schedules.listRuns(searchParams?.get("scheduleId") || undefined) });
      return true;
    }
    const scheduleActionMatch = pathname.match(/^\/schedules\/([^/]+)\/(pause|resume|run)$/);
    if (scheduleActionMatch && req.method === "POST") {
      const id = decodeURIComponent(scheduleActionMatch[1]);
      const action = scheduleActionMatch[2];
      const result = action === "pause" ? await schedules.pause(id) : action === "resume" ? await schedules.resume(id) : await schedules.runNow(id);
      if (!result) {
        sendError(res, 404, "schedule not found", "not_found");
        return true;
      }
      sendJson(res, 200, action === "run" ? { schedule: schedules.get(id), run: result } : { schedule: result });
      return true;
    }
    const scheduleMatch = pathname.match(/^\/schedules\/([^/]+)$/);
    if (scheduleMatch && req.method === "DELETE") {
      const id = decodeURIComponent(scheduleMatch[1]);
      if (!(await schedules.remove(id))) {
        sendError(res, 404, "schedule not found", "not_found");
        return true;
      }
      sendJson(res, 200, { deleted: true, id });
      return true;
    }
    if (req.method === "GET" && (pathname === "/usage" || pathname === "/api/usage/summary")) {
      const sessions = store.list();
      const totals = sessions.reduce((result, session) => {
        const usage = session.usage ?? {};
        const total = usage.total ?? usage;
        for (const key of ["inputTokens", "outputTokens", "cachedInputTokens", "reasoningTokens", "totalTokens"]) {
          const value = Number(total?.[key] ?? usage?.[key] ?? 0);
          if (Number.isFinite(value)) result[key] = (result[key] ?? 0) + value;
        }
        return result;
      }, {});
      sendJson(res, 200, { sessions: sessions.map(({ id, title, usage }) => ({ id, title, usage })), totals });
      return true;
    }
    const resumeSessionId = matchSessionChild(pathname, "/resume");
    if (req.method === "POST" && resumeSessionId) {
      sendJson(res, 200, { session: await sessions.resumeSession(resumeSessionId) });
      return true;
    }
    const runtimeConfigSessionId = matchSessionChild(pathname, "/runtime-config");
    if (req.method === "PUT" && runtimeConfigSessionId) {
      const body = await readJsonBody(req);
      sendJson(res, 200, { session: await sessions.updateRuntimeConfig(runtimeConfigSessionId, body) });
      return true;
    }
    const forkSessionId = matchSessionChild(pathname, "/fork");
    if (req.method === "POST" && forkSessionId) {
      const body = await readJsonBody(req);
      sendJson(res, 201, { session: await sessions.forkSession(forkSessionId, body) });
      return true;
    }
    const steerSessionId = matchSessionChild(pathname, "/steer");
    if (req.method === "POST" && steerSessionId) {
      const body = await readJsonBody(req);
      sendJson(res, 200, { session: await sessions.steerSession(steerSessionId, body.prompt, body.attachmentIds ?? []) });
      return true;
    }
    const changesDiffSessionId = matchSessionChild(pathname, "/changes/diff");
    if (req.method === "GET" && changesDiffSessionId) {
      const session = store.get(changesDiffSessionId);
      if (!session) {
        sendError(res, 404, "session not found", "not_found");
        return true;
      }
      sendJson(res, 200, await changes.diff(session.workspacePath));
      return true;
    }
    const changesSessionId = matchSessionChild(pathname, "/changes");
    if (req.method === "GET" && changesSessionId) {
      const session = store.get(changesSessionId);
      if (!session) {
        sendError(res, 404, "session not found", "not_found");
        return true;
      }
      sendJson(res, 200, await changes.status(session.workspacePath));
      return true;
    }
    const attachmentSessionId = matchSessionChild(pathname, "/attachments");
    if (req.method === "POST" && attachmentSessionId) {
      const body = await readJsonBody(req, { limitBytes: 16 * 1024 * 1024 });
      sendJson(res, 201, { attachment: await sessions.addAttachment(attachmentSessionId, body) });
      return true;
    }
    const attachmentDownload = matchSessionAction(pathname, "attachments");
    if (req.method === "GET" && attachmentDownload) {
      const attachment = await sessions.readAttachment(attachmentDownload.sessionId, attachmentDownload.requestId);
      const inline = attachment.mimeType?.startsWith("image/") || searchParams?.get("inline") === "1";
      res.writeHead(200, {
        "Content-Type": attachment.mimeType || "application/octet-stream",
        "Content-Length": attachment.data.length,
        "Content-Disposition": `${inline ? "inline" : "attachment"}; filename="${attachment.name}"`,
        "Cache-Control": "private, max-age=300",
      });
      res.end(attachment.data);
      return true;
    }
    if (req.method === "GET" && attachmentSessionId) {
      const session = store.get(attachmentSessionId);
      if (!session) {
        sendError(res, 404, "session not found", "not_found");
        return true;
      }
      sendJson(res, 200, { attachments: session.attachments ?? [] });
      return true;
    }
    const turnSessionId = matchSessionChild(pathname, "/turns");
    if (req.method === "POST" && turnSessionId) {
      const body = await readJsonBody(req);
      const session = await sessions.runTurn(turnSessionId, body.prompt, Array.isArray(body.attachmentIds) ? body.attachmentIds.map(String) : []);
      sendJson(res, 200, { session });
      return true;
    }
    const interruptSessionId = matchSessionChild(pathname, "/interrupt");
    if (req.method === "POST" && interruptSessionId) {
      const session = await sessions.interrupt(interruptSessionId);
      sendJson(res, 200, { session });
      return true;
    }
    const requestsSessionId = matchSessionChild(pathname, "/requests");
    if (req.method === "GET" && requestsSessionId) {
      if (!store.get(requestsSessionId)) {
        sendError(res, 404, "session not found", "not_found");
        return true;
      }
      sendJson(res, 200, { requests: sessions.listRequests(requestsSessionId) });
      return true;
    }
    const approvalRoute = matchSessionAction(pathname, "approvals");
    if (req.method === "POST" && approvalRoute) {
      const body = await readJsonBody(req);
      const decision = body.decision === "approve" || body.decision === "accept" ? "approve" : "deny";
      const result = await sessions.resolveApproval(approvalRoute.sessionId, approvalRoute.requestId, decision, body.note);
      sendJson(res, 200, result);
      return true;
    }
    const questionRoute = matchSessionAction(pathname, "questions");
    if (req.method === "POST" && questionRoute) {
      const body = await readJsonBody(req);
      const entry = sessions.listRequests(questionRoute.sessionId).find((item) => item.id === questionRoute.requestId);
      if (!entry) {
        sendError(res, 404, "question request not found", "not_found");
        return true;
      }
      const answers = {};
      for (const question of entry.payload.questions ?? []) {
        const provided = body.answers?.[question.id] ?? body.answer;
        const list = Array.isArray(provided) ? provided.map(String) : provided === undefined ? [] : [String(provided)];
        answers[question.id] = { answers: list };
      }
      const result = await sessions.answerQuestion(questionRoute.sessionId, questionRoute.requestId, answers);
      sendJson(res, 200, result);
      return true;
    }
    const eventsSessionId = matchSessionChild(pathname, "/events");
    if (req.method === "GET" && eventsSessionId) {
      const session = store.get(eventsSessionId);
      if (!session) {
        sendError(res, 404, "session not found", "not_found");
        return true;
      }
      res.writeHead(200, {
        "Content-Type": "text/event-stream; charset=utf-8",
        "Cache-Control": "no-store",
        Connection: "keep-alive",
      });
      const send = (entry) => res.write(`data: ${JSON.stringify(entry)}\n\n`);
      const requestedLimit = Number(searchParams?.get("limit") || 500);
      const limit = Number.isFinite(requestedLimit) ? Math.max(1, Math.min(500, Math.trunc(requestedLimit))) : 500;
      for (const entry of bus.history(eventsSessionId, limit)) send(entry);
      const unsubscribe = bus.subscribe(eventsSessionId, send);
      const keepAlive = setInterval(() => res.write(": ping\n\n"), 25000);
      req.on("close", () => {
        clearInterval(keepAlive);
        unsubscribe();
      });
      return true;
    }
    const sessionId = matchSessionChild(pathname, "");
    if (sessionId) {
      const session = store.get(sessionId);
      if (!session) {
        sendError(res, 404, "session not found", "not_found");
        return true;
      }
      if (req.method === "GET") {
        sendJson(res, 200, { session });
        return true;
      }
      if (req.method === "DELETE") {
        sessions.remove(sessionId);
        sendJson(res, 200, { deleted: true, id: sessionId });
        return true;
      }
    }
    return false;
  }

  async function handleFilesystem(req, res, pathname, searchParams) {
    if (req.method === "GET" && pathname === "/filesystem/roots") {
      sendJson(res, 200, await files.roots());
      return true;
    }
    if (req.method === "GET" && pathname === "/filesystem/list") {
      sendJson(res, 200, await files.list(searchParams.get("path")));
      return true;
    }
    if (req.method === "GET" && pathname === "/filesystem/read") {
      sendJson(res, 200, await files.read(searchParams.get("path")));
      return true;
    }
    return false;
  }

  const server = http.createServer(async (req, res) => {
    const host = req.headers.host ?? `${config.host}:${config.port}`;
    let url;
    try {
      url = new URL(req.url ?? "/", `http://${host}`);
    } catch {
      sendError(res, 400, "invalid request url", "invalid_request");
      return;
    }
    const pathname = url.pathname;
    try {
      if (req.method === "OPTIONS") {
        res.writeHead(204);
        res.end();
        return;
      }
      if (await handlePublic(req, res, pathname)) return;
      if (!isAuthorized(req, config)) {
        sendError(res, 401, "unauthorized", "unauthorized");
        return;
      }
      if (await handleSessions(req, res, pathname, url.searchParams)) return;
      if (await handleFilesystem(req, res, pathname, url.searchParams)) return;
      sendError(res, 404, `no route for ${req.method} ${pathname}`, "not_found");
    } catch (error) {
      const status = error?.statusCode ?? 500;
      if (status >= 500) logger.error("request failed", req.method, pathname, error?.stack ?? String(error));
      if (!res.headersSent) sendError(res, status, error?.message ?? "internal error", error?.code ?? "error");
      else res.end();
    }
  });

  async function close() {
    schedules.stop();
    await sessions.shutdown();
    await new Promise((resolve) => server.close(resolve));
  }

  return { server, close, store, bus, sessions, files, captcha, schedules, changes };
}
