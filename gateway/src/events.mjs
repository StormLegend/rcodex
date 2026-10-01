import fs from "node:fs";
import path from "node:path";
import { pageLimit, sequence, invalidCursor } from "./pagination.mjs";

// Append-only numbered segments. Live memory is bounded; disk history is not
// silently evicted when the UI's recent-event window fills up.
const SEGMENT_SIZE = 256;
export function createEventBus({ bufferSize = 500, dataDir } = {}) {
  const states = new Map(), subscribers = new Map(), globalSubscribers = new Set();
  const directory = (id) => dataDir && path.join(dataDir, "event-segments", encodeURIComponent(id));
  const segmentPath = (id, index) => path.join(directory(id), String(index).padStart(12, "0") + ".jsonl");
  const parse = (file) => fs.readFileSync(file, "utf8").split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
  function append(id, state, message) {
    const seq = state.last + 1;
    const entry = { ...message, sessionId: id, eventId: String(seq), timestamp: message.timestamp ?? new Date().toISOString() };
    if (dataDir) {
      fs.mkdirSync(directory(id), { recursive: true, mode: 0o700 });
      fs.appendFileSync(segmentPath(id, Math.floor((seq - 1) / SEGMENT_SIZE)), JSON.stringify(entry) + "\n", { mode: 0o600 });
    } else state.all.push(entry);
    state.last = seq;
    state.recent.push(entry);
    if (state.recent.length > bufferSize) state.recent.shift();
    return entry;
  }
  function load(id) {
    if (states.has(id)) return states.get(id);
    const state = { last: 0, recent: [], all: [] };
    if (dataDir && fs.existsSync(directory(id))) {
      const names = fs.readdirSync(directory(id)).filter((name) => /^\d{12}\.jsonl$/.test(name)).sort();
      if (names.length) {
        const tail = parse(path.join(directory(id), names.at(-1)));
        state.last = Number(tail.at(-1)?.eventId || 0);
        for (const name of names.slice(-Math.ceil(bufferSize / SEGMENT_SIZE) - 1)) state.recent.push(...parse(path.join(directory(id), name)));
        state.recent = state.recent.slice(-bufferSize);
      }
    } else if (dataDir) {
      const legacy = path.join(dataDir, "events", encodeURIComponent(id) + ".jsonl");
      if (fs.existsSync(legacy)) {
        // Preserve the original file. Interrupted migrations can be retried by
        // operators; existing segments are never overwritten.
        for (const entry of parse(legacy)) append(id, state, entry);
      }
    }
    states.set(id, state);
    return state;
  }
  function page(id, { limit = 100, before, after } = {}) {
    limit = pageLimit(limit);
    before = sequence(before); after = sequence(after);
    if (before !== undefined && after !== undefined) throw invalidCursor("use either before or after");
    const state = load(id);
    const end = before === undefined ? state.last : Math.min(state.last, before - 1);
    const start = after === undefined ? Math.max(1, end - limit + 1) : after + 1;
    const stop = Math.min(end, start + limit - 1);
    let entries = [];
    if (stop >= start) {
      if (!dataDir) entries = state.all.filter((e) => Number(e.eventId) >= start && Number(e.eventId) <= stop);
      else for (let i = Math.floor((start - 1) / SEGMENT_SIZE); i <= Math.floor((stop - 1) / SEGMENT_SIZE); i++) {
        entries.push(...parse(segmentPath(id, i)).filter((e) => Number(e.eventId) >= start && Number(e.eventId) <= stop));
      }
    }
    return { entries, nextBefore: entries.length && start > 1 ? entries[0].eventId : undefined,
      nextAfter: entries.at(-1)?.eventId ?? (after === undefined ? undefined : String(after)),
      hasMore: after === undefined ? start > 1 : stop < state.last, total: state.last };
  }
  function emit(id, message) {
    const entry = append(id, load(id), message);
    for (const fn of [...(subscribers.get(id) ?? []), ...globalSubscribers]) {
      try { fn(entry); } catch { /* a disconnected client cannot break the runtime */ }
    }
    return entry;
  }
  function subscribe(id, fn) {
    if (!subscribers.has(id)) subscribers.set(id, new Set());
    subscribers.get(id).add(fn);
    return () => subscribers.get(id)?.delete(fn);
  }
  function subscribeAll(fn) { globalSubscribers.add(fn); return () => globalSubscribers.delete(fn); }
  function history(id, limit = bufferSize) { return load(id).recent.slice(-limit); }
  function forget(id) {
    for (const fn of subscribers.get(id) ?? []) fn({ type: "session-deleted", sessionId: id, timestamp: new Date().toISOString() });
    subscribers.delete(id); states.delete(id);
    if (dataDir) {
      fs.rmSync(directory(id), { force: true, recursive: true });
      fs.rmSync(path.join(dataDir, "events", encodeURIComponent(id) + ".jsonl"), { force: true });
    }
  }
  return { emit, history, historyPage: page, subscribe, subscribeAll, forget };
}
