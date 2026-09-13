#!/usr/bin/env node
// Minimal stand-in for `codex app-server`, used by the gateway tests.
//
// Speaks the same newline-delimited JSON-RPC framing and emits the same method
// names as the real binary for the flows the gateway implements:
//   - initialize / thread/start / turn/start / turn/interrupt
//   - notifications: thread/status/changed, item/agentMessage/delta,
//     item/completed, turn/completed
//   - server→client requests: item/commandExecution/requestApproval,
//     item/tool/requestUserInput (only when approvalPolicy is "on-request")
import { createInterface } from "node:readline";

const THREAD_ID = "thread-fake-1";
const lines = createInterface({ input: process.stdin });

let approvalPolicy = "never";

function send(payload) {
  process.stdout.write(`${JSON.stringify(payload)}\n`);
}

function notify(method, params) {
  send({ jsonrpc: "2.0", method, params });
}

function finishTurn(text) {
  setTimeout(() => {
    notify("thread/status/changed", { threadId: THREAD_ID, status: { type: "active" } });
    notify("item/agentMessage/delta", { threadId: THREAD_ID, delta: text });
    notify("item/completed", { threadId: THREAD_ID, item: { type: "agentMessage", text } });
    notify("turn/completed", { threadId: THREAD_ID, turnId: "turn-1" });
    notify("thread/status/changed", { threadId: THREAD_ID, status: { type: "idle" } });
  }, 10);
}

lines.on("line", (line) => {
  const text = line.trim();
  if (!text) return;
  const message = JSON.parse(text);
  const { id, method, params, result } = message;

  // Responses to the server→client requests we issued below.
  if (id === "srv-approval-1" && result) {
    notify("item/approvalResolved", { threadId: THREAD_ID, decision: result.decision });
    finishTurn(`approval:${result.decision}`);
    return;
  }
  if (id === "srv-question-1" && result) {
    notify("item/answersReceived", { threadId: THREAD_ID, answers: result.answers });
    finishTurn("answered");
    return;
  }

  if (method === "initialize") {
    send({ jsonrpc: "2.0", id, result: { userAgent: "fake-codex-app-server/1.0" } });
    return;
  }
  if (method === "thread/start") {
    approvalPolicy = params?.approvalPolicy ?? "never";
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
          approvalPolicy,
        },
      },
    });
    return;
  }
  if (method === "turn/start") {
    send({ jsonrpc: "2.0", id, result: { turn: { id: "turn-1" } } });
    const prompt = params?.input?.[0]?.text ?? "";
    if (approvalPolicy !== "never" && prompt.includes("ask-approval")) {
      send({
        jsonrpc: "2.0",
        id: "srv-approval-1",
        method: "item/commandExecution/requestApproval",
        params: {
          threadId: THREAD_ID,
          turnId: "turn-1",
          itemId: "item-1",
          reason: "需要执行 rm -rf /tmp/rcodex-demo",
          command: "rm -rf /tmp/rcodex-demo",
          cwd: params?.cwd,
        },
      });
      return;
    }
    if (approvalPolicy !== "never" && prompt.includes("ask-question")) {
      send({
        jsonrpc: "2.0",
        id: "srv-question-1",
        method: "item/tool/requestUserInput",
        params: {
          threadId: THREAD_ID,
          questions: [
            {
              id: "q1",
              header: "目标文件",
              question: "要写入哪个文件？",
              options: [{ label: "a.txt", description: "第一个候选" }, { label: "b.txt", description: "第二个候选" }],
            },
          ],
        },
      });
      return;
    }
    finishTurn(`收到：${prompt}`);
    return;
  }
  if (method === "turn/interrupt") {
    send({ jsonrpc: "2.0", id, result: {} });
    return;
  }
  send({ jsonrpc: "2.0", id, error: { code: -32601, message: `unsupported method ${method}` } });
});
