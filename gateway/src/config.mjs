import fs from "node:fs";
import os from "node:os";
import path from "node:path";

export function parseEnvFile(text) {
  const vars = {};
  for (const rawLine of String(text).split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) continue;
    const withoutExport = line.startsWith("export ") ? line.slice(7).trim() : line;
    const eq = withoutExport.indexOf("=");
    if (eq <= 0) continue;
    const key = withoutExport.slice(0, eq).trim();
    let value = withoutExport.slice(eq + 1).trim();
    if (
      (value.startsWith('"') && value.endsWith('"') && value.length >= 2) ||
      (value.startsWith("'") && value.endsWith("'") && value.length >= 2)
    ) {
      value = value.slice(1, -1);
    }
    vars[key] = value;
  }
  return vars;
}

function splitList(value) {
  return String(value ?? "")
    .split(/\s+/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function resolveList(value, fallback) {
  const items = splitList(value).map((item) => path.resolve(item));
  return items.length ? items : fallback;
}

export function loadConfig({ env = process.env, envFile } = {}) {
  const fileVars = envFile && fs.existsSync(envFile) ? parseEnvFile(fs.readFileSync(envFile, "utf8")) : {};
  const get = (key, fallback = "") => {
    const value = env[key];
    if (value !== undefined && value !== "") return value;
    const fromFile = fileVars[key];
    if (fromFile !== undefined && fromFile !== "") return fromFile;
    return fallback;
  };

  const home = os.homedir();
  const dataDir = path.resolve(get("GATEWAY_DATA_DIR", path.join(home, ".rcodex", "gateway", "data")));
  const config = {
    version: "0.1.0",
    name: get("GATEWAY_NAME", "rcodex-gateway"),
    host: get("GATEWAY_HOST", "127.0.0.1"),
    port: Number(get("GATEWAY_PORT", "8787")),
    dataDir,
    allowedPaths: resolveList(get("GATEWAY_ALLOWED_PATHS", ""), [path.resolve(home)]),
    codexCommand: get("CODEX_COMMAND", "codex"),
    codexModels: splitList(get("CODEX_AVAILABLE_MODELS", "")),
    codexAppServerStartupTimeoutMs: Number(get("CODEX_APP_SERVER_STARTUP_TIMEOUT_MS", "60000")),
    defaultPermissionMode: get("GATEWAY_PERMISSION_MODE", "full"),
    authUsername: get("GATEWAY_AUTH_USERNAME", "admin"),
    authPassword: get("GATEWAY_AUTH_PASSWORD", ""),
    authToken: get("GATEWAY_AUTH_TOKEN", ""),
    envFile,
  };

  const problems = [];
  if (!config.authPassword) problems.push("GATEWAY_AUTH_PASSWORD is empty");
  if (!config.authToken) problems.push("GATEWAY_AUTH_TOKEN is empty");
  if (!Number.isFinite(config.port) || config.port <= 0) problems.push("GATEWAY_PORT is invalid");
  if (!config.codexCommand) problems.push("CODEX_COMMAND is empty");
  if (problems.length) {
    const error = new Error(`invalid configuration: ${problems.join(", ")}`);
    error.code = "invalid_config";
    throw error;
  }

  return config;
}
