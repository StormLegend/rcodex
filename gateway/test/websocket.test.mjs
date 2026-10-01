import assert from "node:assert/strict";
import test from "node:test";
import http from "node:http";
import { WebSocket } from "ws";
import { createEventBus } from "../src/events.mjs";
import { attachWebSocket } from "../src/websocket.mjs";

test("authenticated websocket replays session events and answers ping", async () => {
  const bus = createEventBus({ bufferSize: 10 });
  bus.emit("s1", { type: "session-status", payload: { status: "completed" } });
  const store = { get: (id) => id === "s1" ? { id } : undefined };
  const config = { name: "test", authToken: "token" };
  const server = http.createServer();
  const socketServer = attachWebSocket(server, { config, bus, store });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const port = server.address().port;
  const ws = new WebSocket(`ws://127.0.0.1:${port}/ws?token=token&sessionId=s1`);
  const messages = [];
  await new Promise((resolve, reject) => {
    ws.on("message", (raw) => {
      const msg = JSON.parse(raw);
      messages.push(msg);
      if (msg.type === "gateway-ready") ws.send(JSON.stringify({ type: "ping" }));
      if (msg.type === "pong") resolve();
    });
    ws.on("error", reject);
  });
  assert.equal(messages[0].type, "gateway-ready");
  assert.ok(messages.some((msg) => msg.type === "session-status"));
  assert.ok(messages.some((msg) => msg.type === "pong"));
  ws.close(); socketServer.close(); await new Promise((resolve) => server.close(resolve));
});
