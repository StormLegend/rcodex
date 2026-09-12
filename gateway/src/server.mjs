import http from "node:http";
import { createCaptchaStore, isAuthorized, validateCredentials } from "./auth.mjs";
import { renderConsoleHtml } from "./console.mjs";
import { createEventBus } from "./events.mjs";
import { createFilesystem } from "./filesystem.mjs";
import { createSessionStore } from "./session-store.mjs";
import { createSessionManager } from "./sessions.mjs";
import { nowIso, readJsonBody, sendError, sendJson } from "./util.mjs";

function matchSessionChild(pathname, suffix) {
  const match = pathname.match(new RegExp(`^/sessions/([^/]+)${suffix}$`));
  return match ? decodeURIComponent(match[1]) : undefined;
}

export function createGatewayServer({ config, logger }) {
  const captcha = createCaptchaStore();
  const bus = createEventBus();
  const store = createSessionStore({ dataDir: config.dataDir });
  store.load();
  const files = createFilesystem({ allowedPaths: config.allowedPaths });
  const sessions = createSessionManager({ config, store, bus, logger });

  async function handlePublic(req, res, pathname) {
    if (req.method === "GET" && pathname === "/health") {
      sendJson(res, 200, { status: "ok", name: config.name, version: config.version, time: nowIso() });
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
      sendJson(res, 200, { name: config.name, version: config.version, models: config.codexModels });
      return true;
    }
    return false;
  }

  async function handleSessions(req, res, pathname) {
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
        reasoningEffort: body.reasoningEffort,
        title: body.title,
        permissionMode: body.permissionMode,
      });
      sendJson(res, 201, { session });
      return true;
    }
    const turnSessionId = matchSessionChild(pathname, "/turns");
    if (req.method === "POST" && turnSessionId) {
      const body = await readJsonBody(req);
      const session = await sessions.runTurn(turnSessionId, body.prompt);
      sendJson(res, 200, { session });
      return true;
    }
    const interruptSessionId = matchSessionChild(pathname, "/interrupt");
    if (req.method === "POST" && interruptSessionId) {
      const session = await sessions.interrupt(interruptSessionId);
      sendJson(res, 200, { session });
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
      for (const entry of bus.history(eventsSessionId)) send(entry);
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
      if (await handleSessions(req, res, pathname)) return;
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
    await sessions.shutdown();
    await new Promise((resolve) => server.close(resolve));
  }

  return { server, close, store, bus, sessions, files, captcha };
}
