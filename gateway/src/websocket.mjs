import { WebSocketServer, WebSocket } from "ws";
import { isAuthorized } from "./auth.mjs";
import { sequence } from "./pagination.mjs";

export function attachWebSocket(server, { config, bus, store }) {
  const wss = new WebSocketServer({ noServer: true, maxPayload: 65536 });
  const send = (ws, value) => {
    if (ws.readyState !== WebSocket.OPEN) return;
    const data = JSON.stringify(value);
    if (ws.bufferedAmount + Buffer.byteLength(data) > 4 * 1024 * 1024) return ws.close(1013, "slow consumer; replay history after reconnect");
    ws.send(data);
  };
  server.on("upgrade", (req, socket, head) => {
    try {
      const url = new URL(req.url, "http://localhost");
      if (url.pathname !== "/ws") { socket.end("HTTP/1.1 404 Not Found\r\nConnection: close\r\n\r\n"); return; }
      if (!isAuthorized(req, config)) { socket.end("HTTP/1.1 401 Unauthorized\r\nConnection: close\r\n\r\n"); return; }
      const id = url.searchParams.get("sessionId");
      if (id && !store.get(id)) { socket.end("HTTP/1.1 404 Not Found\r\nConnection: close\r\n\r\n"); return; }
      const after = sequence(url.searchParams.get("after"));
      if (after !== undefined && !id) throw new Error("after requires sessionId");
      wss.handleUpgrade(req, socket, head, (ws) => {
        ws.alive = true;
        ws.on("error", () => {});
        ws.on("pong", () => { ws.alive = true; });
        send(ws, { type: "gateway-ready", timestamp: new Date().toISOString(), payload: { gatewayName: config.name, supportedMessages: ["ping"], sessionId: id } });
        if (id) {
          const page = bus.historyPage(id, { limit: 500, after });
          for (const entry of page.entries) send(ws, entry);
          send(ws, { type: "history-page", sessionId: id, payload: { nextAfter: page.nextAfter, hasMore: after !== undefined && page.hasMore } });
        }
        const off = id ? bus.subscribe(id, (e) => send(ws, e)) : bus.subscribeAll((e) => send(ws, e));
        ws.on("close", off);
        ws.on("message", (raw) => {
          try {
            const msg = JSON.parse(raw.toString());
            send(ws, msg.type === "ping" ? { type: "pong", timestamp: new Date().toISOString() } : { type: "error", payload: { code: "unsupported_message" } });
          } catch { send(ws, { type: "error", payload: { code: "invalid_json" } }); }
        });
      });
    } catch { socket.end("HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n"); }
  });
  const timer = setInterval(() => {
    for (const ws of wss.clients) { if (!ws.alive) ws.terminate(); else { ws.alive = false; ws.ping(); } }
  }, 30000);
  timer.unref();
  return { get size() { return wss.clients.size; }, close() { clearInterval(timer); for (const ws of wss.clients) ws.terminate(); wss.close(); } };
}
