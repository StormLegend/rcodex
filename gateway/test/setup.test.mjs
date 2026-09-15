import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { discoverCodexProviders, generateGatewayEnv, runSetup } from "../src/setup.mjs";
import { parseEnvFile } from "../src/config.mjs";

test("setup discovers providers from Codex config and generates a usable env file", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "rcodex-setup-"));
  const codexHome = path.join(root, ".codex");
  fs.mkdirSync(codexHome);
  fs.writeFileSync(path.join(codexHome, "config.toml"), [
    'model_provider = "deepseek"',
    'model = "deepseek-flash"',
    "",
    "[model_providers.deepseek]",
    'name = "DeepSeek"',
    'env_key = "DEEPSEEK_API_KEY"',
    'base_url = "https://api.deepseek.com/v1"',
    'wire_api = "responses"',
    "",
    "[model_providers.custom]",
    'name = "NewAPI"',
    'env_key = "NEWAPI_API_KEY"',
    'base_url = "https://newapi.example/v1"',
    'wire_api = "responses"',
  ].join("\n"));
  const discovered = await runSetup({ codexHome, envFile: path.join(root, "gateway.env"), workspacePath: root, nonInteractive: true, network: false, password: "secret", token: "token" });
  const vars = parseEnvFile(fs.readFileSync(discovered.envFile, "utf8"));
  assert.equal(vars.GATEWAY_AUTH_PASSWORD, "secret");
  assert.equal(vars.CODEX_DEFAULT_MODEL_PROVIDER, "deepseek");
  assert.deepEqual(JSON.parse(vars.CODEX_MODEL_PROVIDERS), { deepseek: ["deepseek-flash"], custom: [] });
  assert.equal(discoverCodexProviders({ codexHome }).providers.length, 2);
});
