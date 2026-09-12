#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { copyFile, mkdir, readFile, rename, stat, writeFile } from "node:fs/promises";
import { homedir } from "node:os";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const SERVER_VULNERABLE = `if (typeof item.text === "string") {
        compact.text = truncateText(item.text);
    }`;

const SERVER_PATCHED = `if (typeof item.text === "string") {
        // 助手终态正文是历史恢复的唯一完整来源，不能沿用调试字段的长度限制。
        compact.text = type === "agentMessage" ? item.text : truncateText(item.text);
    }`;

const SESSION_RUNTIME_CONFIG_VULNERABLE = `                await this.appServerClient.updateThreadSettings(getProviderSessionId(session), {
                    model: runtimeConfig.model ?? null,
                    reasoningEffort: runtimeConfig.reasoningEffort ?? null,
                    serviceTier: runtimeConfig.serviceTier ?? null,
                });`;

const SESSION_RUNTIME_CONFIG_PATCHED = `                try {
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
                    // app-server restart 后旧 thread 可能已被回收；保存配置，下一轮续聊会重建 thread。
                    console.warn(\`[gateway] Codex thread 不存在，延后应用会话运行配置: sessionId=\${session.id}\`);
                }`;

// 上游 1.4.37 已自带等价修复（区分「thread 已不存在」与其他错误、非该类错误照抛），
// 只是日志文案不同。它只用于识别，不参与改写。
const SESSION_RUNTIME_CONFIG_UPSTREAM_1437 = `                try {
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
                }`;

const CONSOLE_VULNERABLE = `if (view) {
    view.text = item.text;
    view.contentBlocks = normalizeAssistantContentBlocks(item.contentBlocks);`;

const CONSOLE_PATCHED = `if (view) {
    const completedLooksTruncated = item.text.endsWith("…")
      && view.text.length > item.text.length;
    if (!completedLooksTruncated) {
      view.text = item.text;
    }
    view.contentBlocks = normalizeAssistantContentBlocks(item.contentBlocks);`;

const CONSOLE_NATIVE_VULNERABLE = `function completedAssistantText(existingText, completedText) {
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
}`;

const CONSOLE_NATIVE_PATCHED = `function completedAssistantText(existingText, completedText) {
  // 事件已经按同一个 item id 配对。旧 Gateway 的截断终态固定为
  // 4000 个正文字符加省略号；显示层转换可能使流式正文与原始前缀不完全一致。
  const completedLooksLegacyTruncated = completedText.length === LEGACY_TRUNCATED_AGENT_MESSAGE_LENGTH
    && completedText.endsWith("…")
    && existingText.length > completedText.length;
  return completedLooksLegacyTruncated ? existingText : completedText;
}`;

const EMPTY_REASONING_DELTA_VULNERABLE = `function isAlwaysDroppedCompactMessage(message) {
    // 命令 stdout 可能来自 cat/grep/账目扫描等大输出。默认客户端只需要命令完成摘要，
    // 实时逐片段下发会把移动网络和 Gateway 带宽打满。
    return getSessionOutputEventType(message) === "item/commandExecution/outputDelta";
}`;

const EMPTY_REASONING_DELTA_PATCHED = `function isAlwaysDroppedCompactMessage(message) {
    const eventType = getSessionOutputEventType(message);
    if (eventType === "item/reasoning/textDelta") {
        // 该流在 Web 客户端会被压缩为空壳事件且不会渲染，继续下发只会挤占历史窗口。
        return true;
    }
    // 命令 stdout 可能来自 cat/grep/账目扫描等大输出。默认客户端只需要命令完成摘要，
    // 实时逐片段下发会把移动网络和 Gateway 带宽打满。
    return eventType === "item/commandExecution/outputDelta";
}`;

const WORKBENCH_HAS_EARLIER_VULNERABLE = `            const rawEvents = sessionManager.listRecentSessionEvents(sessionWorkbenchRoute.sessionId, CONSOLE_WORKBENCH_RAW_EVENT_LIMIT);
            const eventPage = buildClientSessionEventPage({
                includeRaw: false,
                limit,
                direction: "latest",
                hasEarlierEntries: () => persistence.hasArchivedSessionHistory(sessionWorkbenchRoute.sessionId),`;

const WORKBENCH_HAS_EARLIER_PATCHED = `            const rawEventWindow = sessionManager.listRecentSessionEvents(sessionWorkbenchRoute.sessionId, CONSOLE_WORKBENCH_RAW_EVENT_LIMIT + 1);
            const hasEarlierRawEvents = rawEventWindow.length > CONSOLE_WORKBENCH_RAW_EVENT_LIMIT;
            const rawEvents = hasEarlierRawEvents
                ? rawEventWindow.slice(-CONSOLE_WORKBENCH_RAW_EVENT_LIMIT)
                : rawEventWindow;
            const oldestRawTimestamp = rawEvents[0]?.message.timestamp || new Date().toISOString();
            const hasEarlierPersistedEvents = sessionManager
                .listRecentSessionEventsBefore(sessionWorkbenchRoute.sessionId, 1, oldestRawTimestamp)
                .length > 0;
            const eventPage = buildClientSessionEventPage({
                includeRaw: false,
                limit,
                direction: "latest",
                hasEarlierEntries: () => hasEarlierRawEvents
                    || hasEarlierPersistedEvents
                    || persistence.hasArchivedSessionHistory(sessionWorkbenchRoute.sessionId),`;

const CONSOLE_RUNTIME_CACHE_VULNERABLE = `?v=console-session-fast-20260813`;
const CONSOLE_RUNTIME_CACHE_PATCHED = `?v=console-runtime-switch-20260820`;

const CONSOLE_ENTRY_VULNERABLE = `<script type="module" src="/console/console.js?v=console-generated-images-visible-20260814"></script>`;
const CONSOLE_ENTRY_1435_VULNERABLE = `<script type="module" src="/console/console.js?v=console-terminal-result-reconcile-20260827"></script>`;
const CONSOLE_ENTRY_RUNTIME_SWITCH_VULNERABLE = `<script type="module" src="/console/console.js?v=console-runtime-switch-20260820"></script>`;
const CONSOLE_ENTRY_PATCHED = `<script type="module" src="/console/console.js?v=console-session-detail-header-20260903"></script>`;

const PATCHES = [
  {
    name: "preserve completed agent message text",
    relativePath: "dist/codex-session-manager.js",
    variants: [
      { vulnerable: SERVER_VULNERABLE, patched: SERVER_PATCHED },
    ],
  },
  {
    name: "preserve runtime model changes when an old Codex thread is gone",
    relativePath: "dist/codex-session-manager.js",
    variants: [
      {
        vulnerable: SESSION_RUNTIME_CONFIG_VULNERABLE,
        patched: SESSION_RUNTIME_CONFIG_PATCHED,
      },
      {
        vulnerable: SESSION_RUNTIME_CONFIG_UPSTREAM_1437,
        patched: SESSION_RUNTIME_CONFIG_UPSTREAM_1437,
        upstreamFixed: true,
      },
    ],
  },
  {
    name: "preserve a longer stream when an old completion is truncated",
    relativePath: "dist/console-assets/console-utils.js",
    variants: [
      { vulnerable: CONSOLE_VULNERABLE, patched: CONSOLE_PATCHED },
      { vulnerable: CONSOLE_NATIVE_VULNERABLE, patched: CONSOLE_NATIVE_PATCHED },
    ],
  },
  {
    name: "drop empty reasoning heartbeat events from history windows",
    relativePath: "dist/event-payload-filter.js",
    variants: [
      {
        vulnerable: EMPTY_REASONING_DELTA_VULNERABLE,
        patched: EMPTY_REASONING_DELTA_PATCHED,
      },
    ],
  },
  {
    name: "preserve earlier-history pagination after the raw workbench window is full",
    relativePath: "dist/index.js",
    variants: [
      {
        vulnerable: WORKBENCH_HAS_EARLIER_VULNERABLE,
        patched: WORKBENCH_HAS_EARLIER_PATCHED,
      },
    ],
  },
  {
    name: "refresh the console model capability module",
    relativePath: "dist/console-assets/console.js",
    variants: [
      {
        vulnerable: CONSOLE_RUNTIME_CACHE_VULNERABLE,
        patched: CONSOLE_RUNTIME_CACHE_PATCHED,
        expectedCounts: [2, 3],
        replaceAll: true,
      },
    ],
  },
  {
    name: "refresh the runtime controller model capability module",
    relativePath: "dist/console-assets/console-session-runtime-config.js",
    variants: [
      { vulnerable: CONSOLE_RUNTIME_CACHE_VULNERABLE, patched: CONSOLE_RUNTIME_CACHE_PATCHED },
    ],
  },
  {
    name: "refresh the console entry module",
    relativePath: "dist/console-assets/index.html",
    variants: [
      { vulnerable: CONSOLE_ENTRY_VULNERABLE, patched: CONSOLE_ENTRY_PATCHED },
      { vulnerable: CONSOLE_ENTRY_1435_VULNERABLE, patched: CONSOLE_ENTRY_PATCHED },
      { vulnerable: CONSOLE_ENTRY_RUNTIME_SWITCH_VULNERABLE, patched: CONSOLE_ENTRY_PATCHED },
    ],
  },
];

function countOccurrences(value, needle) {
  if (!needle) return 0;
  return value.split(needle).length - 1;
}

function expectedCounts(variant) {
  return variant.expectedCounts ?? [variant.expectedCount ?? 1];
}

function hasExpectedCount(count, variant) {
  return expectedCounts(variant).includes(count);
}

function timestamp() {
  return new Date().toISOString().replaceAll(":", "-").replaceAll(".", "-");
}

function globalGatewayPath() {
  const npmRoot = execFileSync("npm", ["root", "-g"], { encoding: "utf8" }).trim();
  return path.join(npmRoot, "@rcodex-lab", "gateway");
}

function parseArguments(argv) {
  const args = {
    target: undefined,
    backupRoot: process.env.RCODEX_PATCH_BACKUP_ROOT,
    verifyOnly: false,
    strict: false,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const current = argv[index];
    if (current === "--target") {
      args.target = argv[++index];
    } else if (current === "--backup-root") {
      args.backupRoot = argv[++index];
    } else if (current === "--verify-only") {
      args.verifyOnly = true;
    } else if (current === "--strict") {
      args.strict = true;
    } else if (current === "--fail-open") {
      args.strict = false;
    } else {
      throw new Error(`Unknown argument: ${current}`);
    }
  }

  args.target = path.resolve(args.target || globalGatewayPath());
  args.backupRoot = path.resolve(
    args.backupRoot || path.join(homedir(), ".local", "state", "rcodex-gateway-maintained", "backups"),
  );
  return args;
}

async function readVersion(target) {
  const packageJson = JSON.parse(await readFile(path.join(target, "package.json"), "utf8"));
  return String(packageJson.version || "unknown");
}

async function atomicWrite(filePath, content) {
  const metadata = await stat(filePath);
  const temporaryPath = `${filePath}.rcodex-patch-${process.pid}.tmp`;
  await writeFile(temporaryPath, content, { encoding: "utf8", mode: metadata.mode });
  await rename(temporaryPath, filePath);
}

export async function patchGateway(options) {
  const target = path.resolve(options.target);
  const backupRoot = path.resolve(options.backupRoot);
  const version = await readVersion(target);
  const plans = [];
  const unknown = [];

  for (const patch of PATCHES) {
    const filePath = path.join(target, patch.relativePath);
    const content = await readFile(filePath, "utf8");
    const states = patch.variants.map((variant) => ({
      ...variant,
      vulnerableCount: variant.upstreamFixed
        ? 0
        : countOccurrences(content, variant.vulnerable),
      patchedCount: countOccurrences(content, variant.patched),
    }));
    const vulnerableStates = states.filter((state) => hasExpectedCount(state.vulnerableCount, state));
    const patchedStates = states.filter((state) => hasExpectedCount(state.patchedCount, state));
    const hasAnyVulnerableText = states.some((state) => state.vulnerableCount > 0);

    if (!hasAnyVulnerableText && patchedStates.length > 0) {
      continue;
    }
    if (vulnerableStates.length === 1 && patchedStates.length === 0) {
      plans.push({ ...patch, ...vulnerableStates[0], filePath, content });
      continue;
    }
    unknown.push(`${patch.relativePath}: ${states.map((state, index) => [
      `variant${index + 1}`,
      `vulnerable=${state.vulnerableCount}`,
      `patched=${state.patchedCount}`,
    ].join(" ")).join("; ")}`);
  }

  if (unknown.length > 0) {
    const message = [
      `Unsupported Gateway layout/version ${version}; no unsafe rewrite was attempted.`,
      ...unknown,
    ].join("\n");
    if (options.strict) throw new Error(message);
    console.warn(`[rcodex-local-patch] WARNING: ${message}`);
    return { status: "unsupported", version, changed: [] };
  }

  if (options.verifyOnly) {
    if (plans.length > 0) {
      throw new Error(
        `Gateway ${version} is still vulnerable: ${plans.map((item) => item.relativePath).join(", ")}`,
      );
    }
    console.log(`[rcodex-local-patch] verified Gateway ${version}`);
    return { status: "verified", version, changed: [] };
  }

  if (plans.length === 0) {
    console.log(`[rcodex-local-patch] already applied to Gateway ${version}`);
    return { status: "already-applied", version, changed: [] };
  }

  const backupDirectory = path.join(backupRoot, version, timestamp());
  const plansByFile = new Map();
  for (const plan of plans) {
    const grouped = plansByFile.get(plan.filePath) || {
      content: plan.content,
      filePath: plan.filePath,
      plans: [],
      relativePath: plan.relativePath,
    };
    grouped.plans.push(plan);
    plansByFile.set(plan.filePath, grouped);
  }
  for (const grouped of plansByFile.values()) {
    const backupPath = path.join(backupDirectory, grouped.relativePath);
    await mkdir(path.dirname(backupPath), { recursive: true });
    await copyFile(grouped.filePath, backupPath);

    let nextContent = grouped.content;
    for (const plan of grouped.plans) {
      nextContent = plan.replaceAll
        ? nextContent.replaceAll(plan.vulnerable, plan.patched)
        : nextContent.replace(plan.vulnerable, plan.patched);
    }
    await atomicWrite(grouped.filePath, nextContent);
  }

  console.log(`[rcodex-local-patch] patched Gateway ${version}`);
  console.log(`[rcodex-local-patch] backup: ${backupDirectory}`);
  for (const plan of plans) {
    console.log(`[rcodex-local-patch] changed: ${plan.relativePath} (${plan.name})`);
  }
  return {
    status: "patched",
    version,
    backupDirectory,
    changed: plans.map((item) => item.relativePath),
  };
}

async function main() {
  const options = parseArguments(process.argv.slice(2));
  await patchGateway(options);
}

const isMain = process.argv[1]
  && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url);

if (isMain) {
  main().catch((error) => {
    console.error(`[rcodex-local-patch] ERROR: ${error.message}`);
    process.exitCode = 1;
  });
}
