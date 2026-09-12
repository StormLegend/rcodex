#!/usr/bin/env node
import { loadConfig } from "./config.mjs";
import { createLogger } from "./logger.mjs";
import { createGatewayServer } from "./server.mjs";

const VERSION = "0.1.0";

function usage() {
  return [
    `rcodex-gateway ${VERSION}`,
    "",
    "Usage:",
    "  rcodex-gateway start          Start the gateway in the foreground",
    "  rcodex-gateway service run    Same as start (for systemd ExecStart)",
    "  rcodex-gateway --version      Print the version",
    "",
    "Configuration is read from environment variables (and RCODEX_GATEWAY_ENV):",
    "  GATEWAY_HOST, GATEWAY_PORT, GATEWAY_NAME, GATEWAY_DATA_DIR,",
    "  GATEWAY_ALLOWED_PATHS, GATEWAY_AUTH_USERNAME, GATEWAY_AUTH_PASSWORD,",
    "  GATEWAY_AUTH_TOKEN, CODEX_COMMAND, CODEX_AVAILABLE_MODELS,",
    "  CODEX_APP_SERVER_STARTUP_TIMEOUT_MS",
  ].join("\n");
}

const argv = process.argv.slice(2);
if (argv.includes("--version") || argv.includes("-v")) {
  console.log(VERSION);
  process.exit(0);
}
if (argv.includes("--help") || argv.includes("-h") || argv[0] === "help") {
  console.log(usage());
  process.exit(0);
}

let config;
try {
  config = loadConfig({ envFile: process.env.RCODEX_GATEWAY_ENV });
} catch (error) {
  console.error(`rcodex-gateway: ${error.message}`);
  process.exit(1);
}

const logger = createLogger({ verbose: process.env.GATEWAY_VERBOSE === "1" });
const gateway = createGatewayServer({ config, logger });

gateway.server.listen(config.port, config.host, () => {
  logger.info("========================================");
  logger.info(` ${config.name} v${config.version} (clean-room)`);
  logger.info("========================================");
  logger.info(` Listen  : http://${config.host}:${config.port}`);
  logger.info(` Console : http://${config.host}:${config.port}/console`);
  logger.info(` Data    : ${config.dataDir}`);
  logger.info(` Codex   : ${config.codexCommand} app-server`);
  logger.info(` Paths   : ${config.allowedPaths.join(", ")}`);
  logger.info(` Models  : ${config.codexModels.join(", ") || "(none configured)"}`);
  logger.info(` Sessions: ${gateway.store.list().length}`);
});

gateway.server.on("error", (error) => {
  logger.error("gateway server error", error?.message ?? String(error));
  process.exitCode = 1;
});

let shuttingDown = false;
async function shutdown(signal) {
  if (shuttingDown) return;
  shuttingDown = true;
  logger.info(`received ${signal}, shutting down`);
  try {
    await gateway.close();
  } catch (error) {
    logger.error("shutdown failed", error?.message ?? String(error));
  }
  process.exit(0);
}

process.on("SIGTERM", () => shutdown("SIGTERM"));
process.on("SIGINT", () => shutdown("SIGINT"));
