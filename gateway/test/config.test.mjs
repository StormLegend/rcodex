import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { loadConfig, parseEnvFile } from "../src/config.mjs";

test("parseEnvFile handles comments, quotes and export", () => {
  const vars = parseEnvFile(
    ["# comment", "GATEWAY_PORT=9999", 'GATEWAY_NAME="my gateway"', "export GATEWAY_AUTH_USERNAME='admin'"].join("\n"),
  );
  assert.deepEqual(vars, { GATEWAY_PORT: "9999", GATEWAY_NAME: "my gateway", GATEWAY_AUTH_USERNAME: "admin" });
});

test("loadConfig fails closed without credentials", () => {
  assert.throws(() => loadConfig({ env: { GATEWAY_PORT: "8787" } }), /GATEWAY_AUTH_PASSWORD/);
});

test("loadConfig reads an env file and resolves allowed paths", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "rcodex-config-"));
  const envFile = path.join(dir, "gateway.env");
  const workspace = path.join(dir, "workspace");
  fs.mkdirSync(workspace);
  fs.writeFileSync(
    envFile,
    [
      "GATEWAY_PORT=8899",
      "GATEWAY_AUTH_PASSWORD=secret",
      "GATEWAY_AUTH_TOKEN=token",
      `GATEWAY_ALLOWED_PATHS=${workspace}`,
      "CODEX_AVAILABLE_MODELS=deepseek-flash deepseek-v4-pro",
    ].join("\n"),
  );
  const config = loadConfig({ env: {}, envFile });
  assert.equal(config.port, 8899);
  assert.deepEqual(config.allowedPaths, [workspace]);
  assert.deepEqual(config.codexModels, ["deepseek-flash", "deepseek-v4-pro"]);
});

test("loadConfig parses per-provider model catalogs and defaults to the first provider", () => {
  const config = loadConfig({
    env: {
      GATEWAY_AUTH_PASSWORD: "secret",
      GATEWAY_AUTH_TOKEN: "token",
      CODEX_MODEL_PROVIDERS: JSON.stringify({
        deepseek: ["deepseek-flash", "deepseek-v4-pro"],
        custom: ["gpt-5.6-sol"],
      }),
    },
  });
  assert.equal(config.codexDefaultProvider, "deepseek");
  assert.deepEqual(config.codexProviders, [
    { provider: "deepseek", models: ["deepseek-flash", "deepseek-v4-pro"] },
    { provider: "custom", models: ["gpt-5.6-sol"] },
  ]);
});
