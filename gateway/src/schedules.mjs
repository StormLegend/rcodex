import fs from "node:fs";
import path from "node:path";
import { newId, nowIso, trimmed } from "./util.mjs";

const MAX_SCHEDULES = 100;
const MAX_RUNS = 500;

const CRON_RANGES = [[0, 59], [0, 23], [1, 31], [1, 12], [0, 7]];

function parseCronField(field, min, max) {
  const values = new Set();
  for (const token of field.split(",")) {
    const [rangeText, stepText] = token.split("/");
    const step = stepText ? Number(stepText) : 1;
    if (!Number.isInteger(step) || step < 1) return undefined;
    let start; let end;
    if (rangeText === "*") { start = min; end = max; }
    else if (/^\d+$/.test(rangeText)) { start = Number(rangeText); end = start; }
    else {
      const range = rangeText.match(/^(\d+)-(\d+)$/);
      if (!range) return undefined;
      start = Number(range[1]); end = Number(range[2]);
    }
    if (start < min || end > max || start > end) return undefined;
    for (let value = start; value <= end; value += step) values.add(value === 7 && max === 7 ? 0 : value);
  }
  return values;
}

function parseCron(expression) {
  const fields = expression.split(/\s+/);
  if (fields.length !== 5) return undefined;
  const parsed = fields.map((field, index) => parseCronField(field, ...CRON_RANGES[index]));
  return parsed.every(Boolean) ? parsed : undefined;
}

function isCronExpressionValid(expression) { return Boolean(parseCron(expression)); }

function zonedParts(date, timezone) {
  const parts = new Intl.DateTimeFormat("en-US", { timeZone: timezone, hour12: false, year: "numeric", month: "numeric", day: "numeric", hour: "numeric", minute: "numeric", weekday: "short" }).formatToParts(date);
  const values = Object.fromEntries(parts.filter((part) => part.type !== "literal").map((part) => [part.type, part.value]));
  return { minute: Number(values.minute), hour: Number(values.hour) % 24, day: Number(values.day), month: Number(values.month), weekday: ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"].indexOf(values.weekday) };
}

function nextCronRunAt(expression, timezone, from) {
  const cron = parseCron(expression);
  if (!cron) throw new Error("schedule cron expression is invalid");
  const start = Math.floor(from / 60_000) * 60_000 + 60_000;
  for (let offset = 0; offset < 366 * 24 * 60; offset += 1) {
    const date = new Date(start + offset * 60_000);
    const parts = zonedParts(date, timezone);
    const dayMatches = cron[2].has(parts.day);
    const weekdayMatches = cron[4].has(parts.weekday);
    const domWildcard = cron[2].size === 31;
    const dowWildcard = cron[4].size === 7;
    const calendarMatch = domWildcard && dowWildcard ? true : domWildcard ? weekdayMatches : dowWildcard ? dayMatches : dayMatches || weekdayMatches;
    if (cron[0].has(parts.minute) && cron[1].has(parts.hour) && cron[3].has(parts.month) && calendarMatch) return date.toISOString();
  }
  throw new Error("schedule cron expression has no future run");
}

function readSnapshot(file) {
  try {
    const parsed = JSON.parse(fs.readFileSync(file, "utf8"));
    return { schedules: Array.isArray(parsed?.schedules) ? parsed.schedules : [], runs: Array.isArray(parsed?.runs) ? parsed.runs : [], channels: Array.isArray(parsed?.channels) ? parsed.channels : [] };
  } catch {
    return { schedules: [], runs: [], channels: [] };
  }
}

export function createScheduleService({ dataDir, sessions, files, logger, tickMs = 1000 }) {
  const file = path.join(dataDir, "schedules.json");
  const snapshot = readSnapshot(file);
  const schedules = new Map(snapshot.schedules.map((item) => [item.id, item]));
  const runs = new Map(snapshot.runs.map((item) => [item.id, item]));
  const channels = new Map(snapshot.channels.map((item) => [item.id, item]));
  let timer;

  function save() {
    fs.mkdirSync(dataDir, { recursive: true });
    const temporary = `${file}.tmp-${process.pid}`;
    fs.writeFileSync(temporary, JSON.stringify({ schedules: [...schedules.values()], runs: [...runs.values()].slice(-MAX_RUNS), channels: [...channels.values()] }, null, 2));
    fs.renameSync(temporary, file);
  }

  function parseTrigger(input) {
    const trigger = input?.trigger;
    if (!trigger || typeof trigger !== "object") throw new Error("schedule trigger is required");
    if (trigger.type === "once") {
      const runAt = new Date(trigger.runAt);
      if (!Number.isFinite(runAt.getTime()) || runAt.getTime() <= Date.now()) throw new Error("schedule once runAt must be in the future");
      return { type: "once", runAt: runAt.toISOString() };
    }
    if (trigger.type === "interval") {
      const intervalSeconds = Number(trigger.intervalSeconds);
      if (!Number.isInteger(intervalSeconds) || intervalSeconds < 60 || intervalSeconds > 31_536_000) throw new Error("schedule intervalSeconds must be between 60 and 31536000");
      return { type: "interval", intervalSeconds };
    }
    if (trigger.type === "cron") {
      const expression = trimmed(trigger.expression);
      if (!expression || expression.split(/\s+/).length !== 5 || !isCronExpressionValid(expression)) throw new Error("schedule cron expression must have five valid fields");
      const timezone = trimmed(trigger.timezone) || "Asia/Shanghai";
      try { new Intl.DateTimeFormat("en-US", { timeZone: timezone }).format(); } catch { throw new Error("schedule timezone is invalid"); }
      return { type: "cron", expression, timezone };
    }
    throw new Error("schedule trigger type must be once, interval, or cron");
  }

  async function normalizeInput(input) {
    if (!input || typeof input !== "object") throw new Error("schedule input is required");
    const name = trimmed(input.name);
    const prompt = trimmed(input.prompt);
    if (!name || !prompt) throw new Error("schedule name and prompt are required");
    const workspacePath = trimmed(input.workspacePath) || undefined;
    if (!workspacePath) throw new Error("schedule workspacePath is required");
    const resolved = await files.resolveAllowed(workspacePath);
    return {
      name: name.slice(0, 120),
      description: trimmed(input.description).slice(0, 500),
      prompt: prompt.slice(0, 20_000),
      workspacePath: resolved.path,
      modelProvider: trimmed(input.modelProvider) || undefined,
      model: trimmed(input.model) || undefined,
      reasoningEffort: trimmed(input.reasoningEffort) || undefined,
      permissionMode: trimmed(input.permissionMode) || undefined,
      notificationChannelIds: Array.isArray(input.notificationChannelIds) ? [...new Set(input.notificationChannelIds.map(String))].slice(0, 5) : [],
      trigger: parseTrigger(input),
    };
  }

  function nextRunAt(schedule, from = Date.now()) {
    if (schedule.trigger.type === "once") return schedule.trigger.runAt;
    if (schedule.trigger.type === "cron") return nextCronRunAt(schedule.trigger.expression, schedule.trigger.timezone, from);
    return new Date(from + schedule.trigger.intervalSeconds * 1000).toISOString();
  }

  async function runSchedule(schedule, plannedAt = schedule.nextRunAt || nowIso()) {
    if (schedule.status !== "active") return undefined;
    const run = { id: newId(), scheduleId: schedule.id, plannedAt, status: "running", createdAt: nowIso() };
    runs.set(run.id, run);
    save();
    try {
      const session = await sessions.createSession({
        workspacePath: schedule.workspacePath,
        prompt: schedule.prompt,
        modelProvider: schedule.modelProvider,
        model: schedule.model,
        reasoningEffort: schedule.reasoningEffort,
        permissionMode: schedule.permissionMode,
        title: `${schedule.name} · ${new Date(plannedAt).toLocaleString("zh-CN")}`,
      });
      const finished = { ...run, status: "started", sessionId: session.id, updatedAt: nowIso() };
      runs.set(run.id, finished);
      if (schedule.trigger.type === "once") {
        schedules.set(schedule.id, { ...schedule, status: "completed", nextRunAt: undefined, updatedAt: nowIso() });
      } else {
        schedules.set(schedule.id, { ...schedule, nextRunAt: nextRunAt(schedule), updatedAt: nowIso() });
      }
      save();
      await notifySchedule(schedule, finished, session);
      return finished;
    } catch (error) {
      const failed = { ...run, status: "failed", error: String(error.message ?? error), updatedAt: nowIso() };
      runs.set(run.id, failed);
      schedules.set(schedule.id, { ...schedule, lastError: failed.error, nextRunAt: schedule.trigger.type === "interval" ? nextRunAt(schedule) : undefined, status: schedule.trigger.type === "once" ? "failed" : schedule.status, updatedAt: nowIso() });
      save();
      logger?.warn(`schedule ${schedule.id} failed: ${failed.error}`);
      return failed;
    }
  }

  async function notifySchedule(schedule, run, session) {
    for (const channelId of schedule.notificationChannelIds ?? []) {
      const channel = channels.get(channelId);
      if (!channel || channel.enabled === false) continue;
      try {
        const response = await fetch(channel.url, {
          method: "POST",
          headers: { "Content-Type": "application/json", "User-Agent": "rcodex-gateway/0.4" },
          body: JSON.stringify({ event: "schedule-run", schedule: { id: schedule.id, name: schedule.name }, run, session: { id: session.id, title: session.title, status: session.status } }),
          signal: AbortSignal.timeout(5000),
        });
        if (!response.ok) logger?.warn(`notification channel ${channelId} returned HTTP ${response.status}`);
      } catch (error) { logger?.warn(`notification channel ${channelId} failed: ${error.message ?? String(error)}`); }
    }
  }

  async function tick() {
    const now = Date.now();
    for (const schedule of schedules.values()) {
      if (schedule.status !== "active" || !schedule.nextRunAt || Date.parse(schedule.nextRunAt) > now) continue;
      await runSchedule(schedule, schedule.nextRunAt);
    }
  }

  function start() {
    if (timer) return;
    for (const schedule of schedules.values()) {
      if (schedule.status === "active" && !schedule.nextRunAt) schedules.set(schedule.id, { ...schedule, nextRunAt: nextRunAt(schedule) });
    }
    save();
    timer = setInterval(() => tick().catch((error) => logger?.warn(`schedule tick failed: ${error.message ?? String(error)}`)), tickMs);
    timer.unref?.();
  }

  function stop() { if (timer) clearInterval(timer); timer = undefined; }

  async function create(input) {
    if (schedules.size >= MAX_SCHEDULES) throw new Error("schedule limit exceeded");
    const normalized = await normalizeInput(input);
    for (const channelId of normalized.notificationChannelIds) if (!channels.has(channelId)) throw new Error(`notification channel not found: ${channelId}`);
    const timestamp = nowIso();
    const schedule = { id: newId(), ...normalized, status: "active", nextRunAt: undefined, createdAt: timestamp, updatedAt: timestamp };
    schedule.nextRunAt = nextRunAt(schedule);
    schedules.set(schedule.id, schedule);
    save();
    return schedule;
  }

  function list() { return [...schedules.values()].sort((a, b) => String(b.updatedAt).localeCompare(String(a.updatedAt))); }
  function get(id) { return schedules.get(id); }
  function listRuns(scheduleId) { return [...runs.values()].filter((run) => !scheduleId || run.scheduleId === scheduleId).sort((a, b) => String(b.createdAt).localeCompare(String(a.createdAt))); }
  async function remove(id) { const deleted = schedules.delete(id); if (deleted) save(); return deleted; }
  async function pause(id) { const schedule = schedules.get(id); if (!schedule) return undefined; const updated = { ...schedule, status: "paused", updatedAt: nowIso() }; schedules.set(id, updated); save(); return updated; }
  async function resume(id) { const schedule = schedules.get(id); if (!schedule) return undefined; const updated = { ...schedule, status: "active", nextRunAt: nextRunAt(schedule), updatedAt: nowIso() }; schedules.set(id, updated); save(); return updated; }
  async function runNow(id) { const schedule = schedules.get(id); if (!schedule) return undefined; return runSchedule(schedule, nowIso()); }

  async function createChannel(input = {}) {
    const url = trimmed(input.url);
    if (!/^https?:\/\//i.test(url)) throw new Error("webhook url must use http or https");
    const channel = { id: newId(), type: "webhook", name: trimmed(input.name) || "Webhook", url, enabled: true, createdAt: nowIso(), updatedAt: nowIso() };
    channels.set(channel.id, channel); save(); return channel;
  }
  function listChannels() { return [...channels.values()].map(({ url, ...safe }) => ({ ...safe, url: url.replace(/(https?:\/\/)([^/]{1,80})/, "$1$2") })); }
  function getChannel(id) { return channels.get(id); }
  async function removeChannel(id) { const deleted = channels.delete(id); if (deleted) save(); return deleted; }
  async function testChannel(id) {
    const channel = channels.get(id); if (!channel) return undefined;
    const response = await fetch(channel.url, { method: "POST", headers: { "Content-Type": "application/json", "User-Agent": "rcodex-gateway/0.4" }, body: JSON.stringify({ event: "test", source: "rcodex-gateway" }), signal: AbortSignal.timeout(5000) });
    return { ok: response.ok, status: response.status };
  }

  start();
  return { create, list, get, listRuns, remove, pause, resume, runNow, start, stop, tick, createChannel, listChannels, getChannel, removeChannel, testChannel };
}
