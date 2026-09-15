import fs from "node:fs";
import path from "node:path";
import { newId, nowIso, trimmed } from "./util.mjs";

const MAX_SCHEDULES = 100;
const MAX_RUNS = 500;

function readSnapshot(file) {
  try {
    const parsed = JSON.parse(fs.readFileSync(file, "utf8"));
    return { schedules: Array.isArray(parsed?.schedules) ? parsed.schedules : [], runs: Array.isArray(parsed?.runs) ? parsed.runs : [] };
  } catch {
    return { schedules: [], runs: [] };
  }
}

export function createScheduleService({ dataDir, sessions, files, logger, tickMs = 1000 }) {
  const file = path.join(dataDir, "schedules.json");
  const snapshot = readSnapshot(file);
  const schedules = new Map(snapshot.schedules.map((item) => [item.id, item]));
  const runs = new Map(snapshot.runs.map((item) => [item.id, item]));
  let timer;

  function save() {
    fs.mkdirSync(dataDir, { recursive: true });
    const temporary = `${file}.tmp-${process.pid}`;
    fs.writeFileSync(temporary, JSON.stringify({ schedules: [...schedules.values()], runs: [...runs.values()].slice(-MAX_RUNS) }, null, 2));
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
    throw new Error("schedule trigger type must be once or interval");
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
      trigger: parseTrigger(input),
    };
  }

  function nextRunAt(schedule, from = Date.now()) {
    if (schedule.trigger.type === "once") return schedule.trigger.runAt;
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

  start();
  return { create, list, get, listRuns, remove, pause, resume, runNow, start, stop, tick };
}
