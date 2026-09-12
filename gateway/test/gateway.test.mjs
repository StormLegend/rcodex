import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { createLogger } from "../src/logger.mjs";
import { createGatewayServer } from "../src/server.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));

function setup() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "rcodex-gw-"));
  const workspace = path.join(root, "workspace");
  const dataDir = path.join(root, "data");
  fs.mkdirSync(workspace);
  fs.writeFileSync(path.join(workspace, "note.txt"), "hello from the workspace");

  const wrapper = path.join(root, "fake-codex");
  fs.writeFileSync(
    wrapper,
    `#!/usr/bin/env bash\nexec "${process.execPath}" "${path.join(here, "fixtures/fake-codex-app-server.mjs")}" "$@"\n`,
  );
  fs.chmodSync(wrapper, 0o755);

  const config = {
    version: "0.1.0-test",
    name: "rcodex-gateway-test",
    host: "127.0.0.1",
    port: 0,
    dataDir,
    allowedPaths: [workspace],
    codexCommand: wrapper,
    codexModels: ["deepseek-flash"],
    codexAppServerStartupTimeoutMs: 15000,
    authUsername: "admin",
    authPassword: "secret",
    authToken: "test-token",
  };
  const gateway = createGatewayServer({ config, logger: createLogger() });
  return { gateway, config, root, workspace };
}

async function listen(gateway) {
  await new Promise((resolve) => gateway.server.listen(0, "127.0.0.1", resolve));
  return `http://127.0.0.1:${gateway.server.address().port}`;
}

async function login(base, gateway) {
  const challenge = gateway.captcha.createChallenge();
  const parts = challenge.expression.match(/(\d+)\s*([+-])\s*(\d+)/);
  const answer = parts[2] === "+" ? Number(parts[1]) + Number(parts[3]) : Number(parts[1]) - Number(parts[3]);
  const response = await fetch(`${base}/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      username: "admin",
      password: "secret",
      captchaId: challenge.captchaId,
      captchaAnswer: String(answer),
    }),
  });
  assert.equal(response.status, 200);
  return (await response.json()).token;
}

test("auth is enforced, files are listable, and a session runs to completion", async () => {
  const { gateway, workspace } = setup();
  const base = await listen(gateway);
  try {
    const unauthorized = await fetch(`${base}/sessions`);
    assert.equal(unauthorized.status, 401);

    const token = await login(base, gateway);
    const authHeaders = { "Content-Type": "application/json", Authorization: `Bearer ${token}` };

    const roots = await (await fetch(`${base}/filesystem/roots`, { headers: authHeaders })).json();
    assert.deepEqual(roots.roots, [workspace]);

    const listing = await (await fetch(`${base}/filesystem/list?path=${encodeURIComponent(workspace)}`, { headers: authHeaders })).json();
    assert.deepEqual(listing.entries.map((entry) => entry.name), ["note.txt"]);

    const read = await (await fetch(`${base}/filesystem/read?path=${encodeURIComponent(path.join(workspace, "note.txt"))}`, { headers: authHeaders })).json();
    assert.equal(read.content, "hello from the workspace");

    const createResponse = await fetch(`${base}/sessions`, {
      method: "POST",
      headers: authHeaders,
      body: JSON.stringify({ workspacePath: workspace, prompt: "hello", model: "deepseek-flash" }),
    });
    assert.equal(createResponse.status, 201);
    const { session } = await createResponse.json();
    assert.equal(session.status, "running");
    assert.equal(session.provider, "codex");

    await new Promise((resolve) => setTimeout(resolve, 250));
    const detail = await (await fetch(`${base}/sessions/${session.id}`, { headers: authHeaders })).json();
    assert.equal(detail.session.status, "completed");

    const events = gateway.bus.history(session.id).map((entry) => entry.payload?.eventType).filter(Boolean);
    assert.ok(events.includes("item/agentMessage/delta"), `expected an agent message delta in ${events.join(",")}`);
    assert.ok(events.includes("turn/completed"));

    const listed = await (await fetch(`${base}/sessions`, { headers: authHeaders })).json();
    assert.equal(listed.sessions.length, 1);

    const turnResponse = await fetch(`${base}/sessions/${session.id}/turns`, {
      method: "POST",
      headers: authHeaders,
      body: JSON.stringify({ prompt: "第二个问题" }),
    });
    assert.equal(turnResponse.status, 200);

    const deleteResponse = await fetch(`${base}/sessions/${session.id}`, { method: "DELETE", headers: authHeaders });
    assert.equal(deleteResponse.status, 200);
    const afterDelete = await (await fetch(`${base}/sessions`, { headers: authHeaders })).json();
    assert.equal(afterDelete.sessions.length, 0);
  } finally {
    await gateway.close();
  }
});

test("the console page and health endpoint are public", async () => {
  const { gateway } = setup();
  const base = await listen(gateway);
  try {
    const health = await (await fetch(`${base}/health`)).json();
    assert.equal(health.status, "ok");
    const consolePage = await fetch(`${base}/console`);
    assert.equal(consolePage.status, 200);
    assert.match(await consolePage.text(), /rcodex-gateway-test/);
    const redirect = await fetch(`${base}/`, { redirect: "manual" });
    assert.equal(redirect.status, 302);
    assert.equal(redirect.headers.get("location"), "/console");
  } finally {
    await gateway.close();
  }
});
