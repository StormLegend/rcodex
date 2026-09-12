import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";

import { patchGateway } from "../patch-gateway.mjs";

const serverFixture = `function truncateText(input, maxLength = 4000) {
    return input.length <= maxLength ? input : input.slice(0, maxLength) + "…";
}
function compactItemPayload(item) {
    const type = String(item.type ?? "");
    const compact = { type, id: item.id };
    if (typeof item.text === "string") {
        compact.text = truncateText(item.text);
    }
    return compact;
}
async function applyRuntimeConfig() {
                await this.appServerClient.updateThreadSettings(getProviderSessionId(session), {
                    model: runtimeConfig.model ?? null,
                    reasoningEffort: runtimeConfig.reasoningEffort ?? null,
                    serviceTier: runtimeConfig.serviceTier ?? null,
                });
}`;

const consoleFixture = `function completeAssistantMessage(item, view) {
  if (view) {
    view.text = item.text;
    view.contentBlocks = normalizeAssistantContentBlocks(item.contentBlocks);
  }
}`;

const eventPayloadFilterFixture = `function isAlwaysDroppedCompactMessage(message) {
    // 命令 stdout 可能来自 cat/grep/账目扫描等大输出。默认客户端只需要命令完成摘要，
    // 实时逐片段下发会把移动网络和 Gateway 带宽打满。
    return getSessionOutputEventType(message) === "item/commandExecution/outputDelta";
}`;

const gatewayIndexFixture = `            const rawEvents = sessionManager.listRecentSessionEvents(sessionWorkbenchRoute.sessionId, CONSOLE_WORKBENCH_RAW_EVENT_LIMIT);
            const eventPage = buildClientSessionEventPage({
                includeRaw: false,
                limit,
                direction: "latest",
                hasEarlierEntries: () => persistence.hasArchivedSessionHistory(sessionWorkbenchRoute.sessionId),`;

async function fixture() {
  const root = await mkdtemp(path.join(tmpdir(), "rcodex-maintained-test-"));
  const target = path.join(root, "gateway");
  const backupRoot = path.join(root, "backups");
  await mkdir(path.join(target, "dist", "console-assets"), { recursive: true });
  await writeFile(
    path.join(target, "package.json"),
    JSON.stringify({ name: "@rcodex-lab/gateway", version: "1.4.31" }),
  );
  await writeFile(path.join(target, "dist", "codex-session-manager.js"), serverFixture);
  await writeFile(
    path.join(target, "dist", "console-assets", "console-utils.js"),
    consoleFixture,
  );
  await writeFile(
    path.join(target, "dist", "event-payload-filter.js"),
    eventPayloadFilterFixture,
  );
  await writeFile(path.join(target, "dist", "index.js"), gatewayIndexFixture);
  await writeFile(
    path.join(target, "dist", "console-assets", "console.js"),
    `import {
  reasoningOptionsForModel,
} from "./console-select.js?v=console-session-fast-20260813";
import { createConsoleSessionRuntimeConfigController } from "./console-session-runtime-config.js?v=console-session-fast-20260813";
import { createConsoleState } from "./console-state.js?v=console-session-fast-20260813";`,
  );
  await writeFile(
    path.join(target, "dist", "console-assets", "console-session-runtime-config.js"),
    `import {
  reasoningOptionsForModel,
} from "./console-select.js?v=console-session-fast-20260813";`,
  );
  await writeFile(
    path.join(target, "dist", "console-assets", "index.html"),
    `<script type="module" src="/console/console.js?v=console-generated-images-visible-20260814"></script>`,
  );
  return { target, backupRoot };
}

async function native1435Fixture() {
  const options = await fixture();
  await writeFile(
    path.join(options.target, "package.json"),
    JSON.stringify({ name: "@rcodex-lab/gateway", version: "1.4.35" }),
  );
  await writeFile(
    path.join(options.target, "dist", "codex-session-manager.js"),
    `if (typeof item.text === "string") {
        // 助手终态正文是历史恢复的唯一完整来源，不能沿用调试字段的长度限制。
        compact.text = type === "agentMessage" ? item.text : truncateText(item.text);
    }
async function applyRuntimeConfig() {
                await this.appServerClient.updateThreadSettings(getProviderSessionId(session), {
                    model: runtimeConfig.model ?? null,
                    reasoningEffort: runtimeConfig.reasoningEffort ?? null,
                    serviceTier: runtimeConfig.serviceTier ?? null,
                });
}`,
  );
  await writeFile(
    path.join(options.target, "dist", "console-assets", "console-utils.js"),
    `const LEGACY_TRUNCATED_AGENT_MESSAGE_LENGTH = 4001;

function completedAssistantText(existingText, completedText) {
  const legacyPrefix = completedText.endsWith("…")
    ? completedText.slice(0, -1)
    : "";
  // 仅兼容旧 Gateway 固定的 4000 字符截断，正常终态仍应覆盖流式草稿。
  return existingText.length > completedText.length
    && completedText.length === LEGACY_TRUNCATED_AGENT_MESSAGE_LENGTH
    && legacyPrefix.length === LEGACY_TRUNCATED_AGENT_MESSAGE_LENGTH - 1
    && existingText.startsWith(legacyPrefix)
    ? existingText
    : completedText;
}`,
  );
  await writeFile(
    path.join(options.target, "dist", "console-assets", "console.js"),
    `import { reasoningOptionsForModel } from "./console-select.js?v=console-session-fast-20260813";
import { createConsoleSessionRuntimeConfigController } from "./console-session-runtime-config.js?v=console-session-fast-20260813";
import { createConsoleState } from "./console-state.js?v=console-service-token-20260826";`,
  );
  await writeFile(
    path.join(options.target, "dist", "console-assets", "index.html"),
    `<script type="module" src="/console/console.js?v=console-terminal-result-reconcile-20260827"></script>`,
  );
  return options;
}

async function native1437Fixture() {
  const options = await fixture();
  await writeFile(
    path.join(options.target, "package.json"),
    JSON.stringify({ name: "@rcodex-lab/gateway", version: "1.4.37" }),
  );
  await writeFile(
    path.join(options.target, "dist", "codex-session-manager.js"),
    `if (typeof item.text === "string") {
        // 助手终态正文是历史恢复的唯一完整来源，不能沿用调试字段的长度限制。
        compact.text = type === "agentMessage" ? item.text : truncateText(item.text);
    }
async function applyRuntimeConfig() {
                try {
                    await this.appServerClient.updateThreadSettings(getProviderSessionId(session), {
                        model: runtimeConfig.model ?? null,
                        reasoningEffort: runtimeConfig.reasoningEffort ?? null,
                        serviceTier: runtimeConfig.serviceTier ?? null,
                    });
                }
                catch (error) {
                    if (!isMissingActiveTurnError(error)) {
                        throw error;
                    }
                    console.warn(\`[gateway] thread \${getProviderSessionId(session)} 不可更新，已延后到下一轮恢复：\${compactErrorMessage(error)}\`);
                }
}`,
  );
  await writeFile(
    path.join(options.target, "dist", "console-assets", "console-utils.js"),
    `const LEGACY_TRUNCATED_AGENT_MESSAGE_LENGTH = 4001;

function completedAssistantText(existingText, completedText) {
  const legacyPrefix = completedText.endsWith("…")
    ? completedText.slice(0, -1)
    : "";
  // 仅兼容旧 Gateway 固定的 4000 字符截断，正常终态仍应覆盖流式草稿。
  return existingText.length > completedText.length
    && completedText.length === LEGACY_TRUNCATED_AGENT_MESSAGE_LENGTH
    && legacyPrefix.length === LEGACY_TRUNCATED_AGENT_MESSAGE_LENGTH - 1
    && existingText.startsWith(legacyPrefix)
    ? existingText
    : completedText;
}`,
  );
  await writeFile(
    path.join(options.target, "dist", "console-assets", "console.js"),
    `import { reasoningOptionsForModel } from "./console-select.js?v=console-session-fast-20260813";
import { createConsoleSessionRuntimeConfigController } from "./console-session-runtime-config.js?v=console-session-fast-20260813";
import { createConsoleState } from "./console-state.js?v=console-service-token-20260826";`,
  );
  await writeFile(
    path.join(options.target, "dist", "console-assets", "console-session-runtime-config.js"),
    `import {
  reasoningOptionsForModel,
} from "./console-select.js?v=console-session-fast-20260813";`,
  );
  await writeFile(
    path.join(options.target, "dist", "console-assets", "index.html"),
    `<script type="module" src="/console/console.js?v=console-session-detail-header-20260903"></script>`,
  );
  return options;
}

test("patches Gateway, verifies it, and is idempotent", async () => {
  const options = { ...(await fixture()), strict: true, verifyOnly: false };
  const first = await patchGateway(options);
  assert.equal(first.status, "patched");
  assert.equal(first.changed.length, 8);

  const server = await readFile(
    path.join(options.target, "dist", "codex-session-manager.js"),
    "utf8",
  );
  assert.match(server, /type === "agentMessage"/);

  const consoleSource = await readFile(
    path.join(options.target, "dist", "console-assets", "console-utils.js"),
    "utf8",
  );
  assert.match(consoleSource, /completedLooksTruncated/);

  const eventPayloadFilter = await readFile(
    path.join(options.target, "dist", "event-payload-filter.js"),
    "utf8",
  );
  assert.match(eventPayloadFilter, /item\/reasoning\/textDelta/);
  assert.match(eventPayloadFilter, /return true/);

  const gatewayIndex = await readFile(
    path.join(options.target, "dist", "index.js"),
    "utf8",
  );
  assert.match(gatewayIndex, /CONSOLE_WORKBENCH_RAW_EVENT_LIMIT \+ 1/);
  assert.match(gatewayIndex, /hasEarlierRawEvents/);
  assert.match(gatewayIndex, /listRecentSessionEventsBefore/);

  const consoleEntry = await readFile(
    path.join(options.target, "dist", "console-assets", "index.html"),
    "utf8",
  );
  assert.match(consoleEntry, /console-session-detail-header-20260903/);

  const consoleMain = await readFile(
    path.join(options.target, "dist", "console-assets", "console.js"),
    "utf8",
  );
  assert.equal((consoleMain.match(/console-runtime-switch-20260820/g) ?? []).length, 3);

  const second = await patchGateway(options);
  assert.equal(second.status, "already-applied");

  const verified = await patchGateway({ ...options, verifyOnly: true });
  assert.equal(verified.status, "verified");
});

test("recognizes the 1.4.35 server fix and patches its legacy replay edge", async () => {
  const options = { ...(await native1435Fixture()), strict: true, verifyOnly: false };
  const first = await patchGateway(options);
  assert.equal(first.status, "patched");
  assert.equal(first.changed.length, 7);

  const server = await readFile(
    path.join(options.target, "dist", "codex-session-manager.js"),
    "utf8",
  );
  assert.match(server, /type === "agentMessage" \? item\.text : truncateText/);

  const consoleSource = await readFile(
    path.join(options.target, "dist", "console-assets", "console-utils.js"),
    "utf8",
  );
  assert.match(consoleSource, /completedLooksLegacyTruncated/);
  assert.doesNotMatch(consoleSource, /existingText\.startsWith\(legacyPrefix\)/);

  const second = await patchGateway(options);
  assert.equal(second.status, "already-applied");

  const verified = await patchGateway({ ...options, verifyOnly: true });
  assert.equal(verified.status, "verified");
});

test("recognizes the 1.4.37 native runtime-config fix and patches the remaining layouts", async () => {
  const options = { ...(await native1437Fixture()), strict: true, verifyOnly: false };
  const first = await patchGateway(options);
  assert.equal(first.status, "patched");
  assert.equal(first.changed.length, 5);

  const server = await readFile(
    path.join(options.target, "dist", "codex-session-manager.js"),
    "utf8",
  );
  assert.match(server, /isMissingActiveTurnError/);
  assert.doesNotMatch(server, /Codex thread 不存在/);

  const consoleSource = await readFile(
    path.join(options.target, "dist", "console-assets", "console-utils.js"),
    "utf8",
  );
  assert.match(consoleSource, /completedLooksLegacyTruncated/);

  const second = await patchGateway(options);
  assert.equal(second.status, "already-applied");

  const verified = await patchGateway({ ...options, verifyOnly: true });
  assert.equal(verified.status, "verified");
});

test("fail-open mode does not rewrite an unknown upstream layout", async () => {
  const options = { ...(await fixture()), strict: false, verifyOnly: false };
  const serverPath = path.join(options.target, "dist", "codex-session-manager.js");
  await writeFile(serverPath, "unexpected upstream source");
  const result = await patchGateway(options);
  assert.equal(result.status, "unsupported");
  assert.equal(await readFile(serverPath, "utf8"), "unexpected upstream source");
});
