#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { homedir } from "node:os";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

function valueAfter(argv, flag) {
  const index = argv.indexOf(flag);
  return index >= 0 ? argv[index + 1] : undefined;
}

function defaultGatewayPath() {
  const npmRoot = execFileSync("npm", ["root", "-g"], { encoding: "utf8" }).trim();
  return path.join(npmRoot, "@rcodex-lab", "gateway");
}

async function main() {
  const argv = process.argv.slice(2);
  const remove = argv.includes("--remove");
  const scriptPath = path.join(path.dirname(fileURLToPath(import.meta.url)), "patch-gateway.mjs");
  const target = path.resolve(valueAfter(argv, "--target") || defaultGatewayPath());
  const configRoot = process.env.XDG_CONFIG_HOME || path.join(homedir(), ".config");
  const dropInDirectory = path.join(
    configRoot,
    "systemd",
    "user",
    "rcodex-gateway.service.d",
  );
  const dropInPath = path.join(dropInDirectory, "local-patches.conf");

  if (remove) {
    await rm(dropInPath, { force: true });
    execFileSync("systemctl", ["--user", "daemon-reload"], { stdio: "inherit" });
    console.log(`[rcodex-local-patch] removed ${dropInPath}`);
    return;
  }

  await mkdir(dropInDirectory, { recursive: true });
  const unit = `[Service]\nExecStartPre=/usr/bin/env node ${scriptPath} --target ${target} --fail-open\n`;
  await writeFile(dropInPath, unit, "utf8");
  execFileSync("systemctl", ["--user", "daemon-reload"], { stdio: "inherit" });
  console.log(`[rcodex-local-patch] installed ${dropInPath}`);
}

main().catch((error) => {
  console.error(`[rcodex-local-patch] ERROR: ${error.message}`);
  process.exitCode = 1;
});

