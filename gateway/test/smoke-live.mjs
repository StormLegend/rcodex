#!/usr/bin/env node
/**
 * Live smoke test: drives a real `codex app-server` through the gateway.
 *
 *   RCODEX_SMOKE=1 CODEX_COMMAND=/path/to/rcodex-codex \
 *     node test/smoke-live.mjs [workspaceDir] [prompt]
 *
 * Not part of `npm test`: it needs a working Codex install and (usually) network
 * access to the configured model provider.
 */
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { createLogger } from "../src/logger.mjs";
import { createGatewayServer } from "../src/server.mjs";

if (process.env.RCODEX_SMOKE !== "1") {
  console.log("skipped: set RCODEX_SMOKE=1 to run the live smoke test");
  process.exit(0);
}

const workspace = path.resolve(process.argv[2] ?? fs.mkdtempSync(path.join(os.tmpdir(), "rcodex-smoke-ws-")));
const prompt = process.argv[3] ?? "只回复两个字：收到";
fs.mkdirSync(workspace, { recursive: true });

const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "rcodex-smoke-data-"));
const config = {
  version: "0.1.0-smoke",
  name: "rcodex-gateway-smoke",
  host: "127.0.0.1",
  port: 0,
  dataDir,
  allowedPaths: [workspace],
  codexCommand: process.env.CODEX_COMMAND ?? "codex",
  codexModels: (process.env.CODEX_AVAILABLE_MODELS ?? "deepseek-flash").split(/\s+/),
  codexAppServerStartupTimeoutMs: 60000,
  authUsername: "admin",
  authPassword: "smoke",
  authToken: "smoke-token",
};

const gateway = createGatewayServer({ config, logger: createLogger() });
await new Promise((resolve) => gateway.server.listen(0, "127.0.0.1", resolve));
const base = `http://127.0.0.1:${gateway.server.address().port}`;
const auth = { "Content-Type": "application/json", Authorization: `Bearer ${config.authToken}` };

console.log(`gateway up at ${base}, workspace ${workspace}`);
const created = await fetch(`${base}/sessions`, {
  method: "POST",
  headers: auth,
  body: JSON.stringify({ workspacePath: workspace, prompt }),
});
const createdBody = await created.json();
if (!created.ok) {
  console.error("session create failed:", created.status, createdBody);
  await gateway.close();
  process.exit(1);
}
const sessionId = createdBody.session.id;
console.log(`session ${sessionId} started (${createdBody.session.modelLabel ?? "?"})`);

const deadline = Date.now() + 180000;
let session = createdBody.session;
while (Date.now() < deadline) {
  await new Promise((resolve) => setTimeout(resolve, 2000));
  const response = await fetch(`${base}/sessions/${sessionId}`, { headers: auth });
  session = (await response.json()).session;
  process.stdout.write(`  status=${session.status}\r`);
  if (session.status === "completed" || session.status === "failed") break;
}
console.log();

const agentMessages = gateway.bus
  .history(sessionId)
  .map((entry) => entry.payload)
  .filter((payload) => payload?.eventType === "item/completed")
  .map((payload) => payload.jsonPayload?.item)
  .filter((item) => item?.type === "agentMessage")
  .map((item) => item.text);

console.log("final status:", session.status);
console.log("agent messages:", agentMessages);
await fetch(`${base}/sessions/${sessionId}`, { method: "DELETE", headers: auth });
await gateway.close();
process.exit(session.status === "completed" ? 0 : 1);
