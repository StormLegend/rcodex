#!/usr/bin/env node
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { loadConfig } from "./config.mjs";
import { createLogger } from "./logger.mjs";
import { createGatewayServer } from "./server.mjs";
import { generateServiceUnit, runSetup } from "./setup.mjs";

const VERSION = "0.3.0";
const home = os.homedir();
const defaultEnvFile = path.join(home, ".rcodex", "gateway", "gateway.env");

function configEnvFile() {
  const configured = process.env.RCODEX_GATEWAY_ENV;
  if (configured) return configured;
  return fs.existsSync(defaultEnvFile) ? defaultEnvFile : undefined;
}

function usage() {
  return [
    `rcodex-gateway ${VERSION}`,
    "",
    "Usage:",
    "  rcodex-gateway setup [--yes] [--no-network] [--install-service]  自动生成配置",
    "  rcodex-gateway config                                          打印脱敏后的有效配置",
    "  rcodex-gateway service install|start|stop|restart|status          管理用户级服务",
    "  rcodex-gateway start                                            前台启动",
    "  rcodex-gateway service run                                      供 systemd 使用",
    "  rcodex-gateway --version                                       打印版本",
    "",
    "Setup options: --gateway-env-file <path> --workspace <path> --port <number> --password <value> --token <value>",
  ].join("\n");
}

function valueAfter(argv, flag) {
  const index = argv.indexOf(flag);
  return index >= 0 ? argv[index + 1] : undefined;
}

async function serviceAction(action) {
  const unit = "rcodex-gateway.service";
  if (!["start", "stop", "restart", "status", "enable", "disable"].includes(action)) throw new Error(`unsupported service action: ${action}`);
  if (action === "install") {
    const envFile = configEnvFile() || defaultEnvFile;
    if (!fs.existsSync(envFile)) {
      const result = await runSetup({ envFile, nonInteractive: true, installService: true });
      console.log(`配置已生成: ${result.envFile}`);
      console.log(`服务已生成: ${result.unitPath}`);
    } else {
      const unitPath = generateServiceUnit({ envFile });
      if (process.platform !== "win32" && fs.existsSync("/run/systemd/system")) {
        execFileSync("systemctl", ["--user", "daemon-reload"], { stdio: "inherit" });
      }
      console.log(`服务已生成: ${unitPath}`);
    }
    return;
  }
  execFileSync("systemctl", ["--user", action, unit], { stdio: "inherit" });
}

async function startGateway() {
  let config;
  try { config = loadConfig({ envFile: configEnvFile() }); }
  catch (error) {
    console.error(`rcodex-gateway: ${error.message}`);
    console.error("请先运行: rcodex-gateway setup --yes --install-service");
    process.exitCode = 1;
    return;
  }
  const logger = createLogger({ verbose: process.env.GATEWAY_VERBOSE === "1" });
  const gateway = createGatewayServer({ config, logger });
  gateway.server.listen(config.port, config.host, () => {
    logger.info(` ${config.name} v${config.version} (open-source)`);
    logger.info(` Listen: http://${config.host}:${config.port}`);
    logger.info(` Console: http://${config.host}:${config.port}/console`);
    logger.info(` Providers: ${(config.codexProviders ?? []).map((entry) => `${entry.provider}(${entry.models.length})`).join(", ") || "(none)"}`);
    gateway.sessions.resumePersistedSessions().catch((error) => logger.warn(`session recovery failed: ${error.message ?? String(error)}`));
  });
  gateway.server.on("error", (error) => { logger.error("gateway server error", error?.message ?? String(error)); process.exitCode = 1; });
  let shuttingDown = false;
  const shutdown = async (signal) => {
    if (shuttingDown) return;
    shuttingDown = true;
    logger.info(`received ${signal}, shutting down`);
    await gateway.close().catch((error) => logger.error("shutdown failed", error?.message ?? String(error)));
    process.exit(0);
  };
  process.on("SIGTERM", () => shutdown("SIGTERM"));
  process.on("SIGINT", () => shutdown("SIGINT"));
}

async function main() {
  const argv = process.argv.slice(2);
  if (argv.includes("--version") || argv.includes("-v")) return console.log(VERSION);
  if (argv.includes("--help") || argv.includes("-h") || argv[0] === "help") return console.log(usage());
  const command = argv[0] || "start";
  if (command === "setup") {
    const result = await runSetup({
      envFile: valueAfter(argv, "--gateway-env-file"), workspacePath: valueAfter(argv, "--workspace"), port: valueAfter(argv, "--port"),
      password: valueAfter(argv, "--password"), token: valueAfter(argv, "--token"), nonInteractive: argv.includes("--yes"),
      network: !argv.includes("--no-network"), installService: argv.includes("--install-service"),
    });
    console.log(`配置已生成: ${result.envFile}`);
    console.log(`默认 Provider: ${result.discovery.defaultProvider}`);
    console.log(`发现模型: ${result.discovery.providers.map((entry) => `${entry.provider}[${entry.models.length}]`).join(", ")}`);
    if (result.unitPath) console.log(`服务已生成: ${result.unitPath}`);
    return;
  }
  if (command === "config") {
    const config = loadConfig({ envFile: configEnvFile() });
    console.log(JSON.stringify({ ...config, authPassword: "***", authToken: "***" }, null, 2));
    return;
  }
  if (command === "service" && argv[1] && argv[1] !== "run") return serviceAction(argv[1]);
  return startGateway();
}

main().catch((error) => { console.error(`rcodex-gateway: ${error.message ?? String(error)}`); process.exitCode = 1; });
