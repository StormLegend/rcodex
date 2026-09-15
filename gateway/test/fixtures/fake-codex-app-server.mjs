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
let modelProvider = "custom";

function send(payload) {
  process.stdout.write(`${JSON.stringify(payload)}\n`);
}

function notify(method, params) {
  send({ jsonrpc: "2.0", method, params });
}

function finishTurn(text, threadId = THREAD_ID) {
  setTimeout(() => {
    notify("thread/status/changed", { threadId, status: { type: "active" } });
    notify("item/agentMessage/delta", { threadId, delta: text });
    notify("thread/tokenUsage/updated", {
      threadId,
      tokenUsage: { total: { inputTokens: 12, outputTokens: 7, totalTokens: 19 } },
    });
    notify("item/completed", { threadId, item: { type: "agentMessage", text } });
    notify("turn/completed", { threadId, turnId: "turn-1" });
    notify("thread/status/changed", { threadId, status: { type: "idle" } });
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
  if (method === "thread/list") {
    send({ jsonrpc: "2.0", id, result: { data: [
      { id: "thread-import-1", preview: "历史任务", cwd: process.cwd(), model: "deepseek-flash", modelProvider: "deepseek", createdAt: 1_700_000_000, updatedAt: 1_700_000_100 },
    ], nextCursor: null } });
    return;
  }
  if (method === "thread/read") {
    send({ jsonrpc: "2.0", id, result: { thread: { id: params?.threadId, preview: "历史任务", cwd: process.cwd(), model: "deepseek-flash", modelProvider: "deepseek", createdAt: 1_700_000_000, updatedAt: 1_700_000_100 } } });
    return;
  }
  if (method === "thread/items/list") {
    send({ jsonrpc: "2.0", id, result: { data: [
      { id: "old-user", type: "userMessage", text: "旧问题", createdAt: 1_700_000_010 },
      { id: "old-agent", type: "agentMessage", text: "旧回答", createdAt: 1_700_000_020 },
    ], nextCursor: null } });
    return;
  }
  if (method === "skills/list") {
    send({ jsonrpc: "2.0", id, result: { skills: [{ name: "review", enabled: true, cwd: params?.cwds?.[0] }] } });
    return;
  }
  if (method === "plugin/list") {
    send({ jsonrpc: "2.0", id, result: { plugins: [{ id: "plugin.demo", name: "Demo Plugin" }] } });
    return;
  }
  if (method === "mcpServerStatus/list") {
    send({ jsonrpc: "2.0", id, result: { data: [{ name: "filesystem", status: "connected" }], nextCursor: null } });
    return;
  }
  if (method === "app/list") {
    send({ jsonrpc: "2.0", id, result: { data: [{ id: "app.demo", name: "Demo App" }], nextCursor: null } });
    return;
  }
  if (method === "hooks/list") {
    send({ jsonrpc: "2.0", id, result: { hooks: [{ name: "pre-commit", enabled: true }] } });
    return;
  }
  if (method === "mcpServer/refresh") {
    send({ jsonrpc: "2.0", id, result: {} });
    return;
  }
  if (method === "thread/start") {
    approvalPolicy = params?.approvalPolicy ?? "never";
    modelProvider = params?.modelProvider ?? "custom";
    send({
      jsonrpc: "2.0",
      id,
      result: {
        thread: {
          id: THREAD_ID,
          model: params?.model ?? "deepseek-flash",
          modelProvider,
          reasoningEffort: "medium",
          cwd: params?.cwd,
          approvalPolicy,
        },
      },
    });
    return;
  }
  if (method === "thread/resume") {
    send({
      jsonrpc: "2.0",
      id,
      result: { thread: { id: THREAD_ID, model: params?.model ?? "deepseek-flash", modelProvider: params?.modelProvider ?? modelProvider, reasoningEffort: "medium" } },
    });
    return;
  }
  if (method === "thread/fork") {
    send({
      jsonrpc: "2.0",
      id,
      result: { thread: { id: `${THREAD_ID}-fork`, model: params?.model ?? "deepseek-flash", modelProvider: params?.modelProvider ?? modelProvider, reasoningEffort: "medium" } },
    });
    return;
  }
  if (method === "thread/settings/update") {
    send({ jsonrpc: "2.0", id, result: { thread: { id: THREAD_ID, model: params?.model, reasoningEffort: params?.reasoningEffort } } });
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
  if (method === "turn/steer") {
    send({ jsonrpc: "2.0", id, result: { turn: { id: "turn-steered" } } });
    finishTurn(`steered:${params?.input?.[0]?.text ?? ""}`, params?.threadId ?? THREAD_ID);
    return;
  }
  if (method === "turn/interrupt") {
    send({ jsonrpc: "2.0", id, result: {} });
    return;
  }
  send({ jsonrpc: "2.0", id, error: { code: -32601, message: `unsupported method ${method}` } });
});
