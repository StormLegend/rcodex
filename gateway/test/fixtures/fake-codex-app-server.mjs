#!/usr/bin/env node
// Minimal stand-in for `codex app-server`, used by the gateway tests.
// Speaks the same newline-delimited JSON-RPC framing and emits the same
// notification names as the real binary for the flows the gateway implements.
import { createInterface } from "node:readline";

const THREAD_ID = "thread-fake-1";
const lines = createInterface({ input: process.stdin });

function send(payload) {
  process.stdout.write(`${JSON.stringify(payload)}\n`);
}

function notify(method, params) {
  send({ jsonrpc: "2.0", method, params });
}

lines.on("line", (line) => {
  const text = line.trim();
  if (!text) return;
  const message = JSON.parse(text);
  const { id, method, params } = message;

  if (method === "initialize") {
    send({ jsonrpc: "2.0", id, result: { userAgent: "fake-codex-app-server/1.0" } });
    return;
  }
  if (method === "thread/start") {
    send({
      jsonrpc: "2.0",
      id,
      result: {
        thread: {
          id: THREAD_ID,
          model: params?.model ?? "deepseek-flash",
          modelProvider: "custom",
          reasoningEffort: "medium",
          cwd: params?.cwd,
        },
      },
    });
    return;
  }
  if (method === "turn/start") {
    send({ jsonrpc: "2.0", id, result: { turn: { id: "turn-1" } } });
    const reply = `收到：${params?.input?.[0]?.text ?? ""}`;
    setTimeout(() => {
      notify("thread/status/changed", { threadId: THREAD_ID, status: { type: "active" } });
      notify("item/agentMessage/delta", { threadId: THREAD_ID, delta: reply });
      notify("item/completed", { threadId: THREAD_ID, item: { type: "agentMessage", text: reply } });
      notify("turn/completed", { threadId: THREAD_ID, turnId: "turn-1" });
      notify("thread/status/changed", { threadId: THREAD_ID, status: { type: "idle" } });
    }, 10);
    return;
  }
  if (method === "turn/interrupt") {
    send({ jsonrpc: "2.0", id, result: {} });
    return;
  }
  send({ jsonrpc: "2.0", id, error: { code: -32601, message: `unsupported method ${method}` } });
});
