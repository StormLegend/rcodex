import fs from "node:fs";
import path from "node:path";

const DEFAULT_BUFFER = 500;

export function createEventBus({ bufferSize = DEFAULT_BUFFER, dataDir } = {}) {
  const buffers = new Map();
  const subscribers = new Map();

  function fileFor(sessionId) {
    return dataDir ? path.join(dataDir, "events", `${encodeURIComponent(String(sessionId))}.jsonl`) : undefined;
  }

  function loadPersisted(sessionId) {
    const file = fileFor(sessionId);
    if (!file || buffers.has(sessionId)) return bufferFor(sessionId);
    try {
      const entries = fs.readFileSync(file, "utf8")
        .split(/\r?\n/)
        .filter(Boolean)
        .map((line) => JSON.parse(line))
        .slice(-bufferSize);
      buffers.set(sessionId, entries);
    } catch {
      buffers.set(sessionId, []);
    }
    return buffers.get(sessionId);
  }

  function bufferFor(sessionId) {
    if (!buffers.has(sessionId)) buffers.set(sessionId, []);
    return buffers.get(sessionId);
  }

  function emit(sessionId, message) {
    const entry = { ...message, sessionId, timestamp: message.timestamp ?? new Date().toISOString() };
    const buffer = loadPersisted(sessionId);
    buffer.push(entry);
    if (buffer.length > bufferSize) buffer.splice(0, buffer.length - bufferSize);
    const file = fileFor(sessionId);
    if (file) {
      try {
        fs.mkdirSync(path.dirname(file), { recursive: true });
        fs.appendFileSync(file, `${JSON.stringify(entry)}\n`);
        if (buffer.length === bufferSize) {
          fs.writeFileSync(`${file}.tmp-${process.pid}`, `${buffer.map((item) => JSON.stringify(item)).join("\n")}\n`);
          fs.renameSync(`${file}.tmp-${process.pid}`, file);
        }
      } catch {
        /* persistence is best effort; live subscribers still receive the event */
      }
    }
    for (const listener of subscribers.get(sessionId) ?? []) {
      try {
        listener(entry);
      } catch {
        /* a broken listener must not break the others */
      }
    }
    return entry;
  }

  function subscribe(sessionId, listener) {
    if (!subscribers.has(sessionId)) subscribers.set(sessionId, new Set());
    subscribers.get(sessionId).add(listener);
    return () => {
      subscribers.get(sessionId)?.delete(listener);
    };
  }

  function history(sessionId, limit = bufferSize) {
    const buffer = loadPersisted(sessionId);
    return buffer.slice(Math.max(0, buffer.length - limit));
  }

  function forget(sessionId) {
    buffers.delete(sessionId);
    for (const listener of subscribers.get(sessionId) ?? []) {
      try {
        listener({ type: "session-deleted", sessionId, timestamp: new Date().toISOString() });
      } catch {
        /* ignore */
      }
    }
    subscribers.delete(sessionId);
    const file = fileFor(sessionId);
    if (file) {
      try { fs.rmSync(file, { force: true }); } catch { /* ignore */ }
    }
  }

  return { emit, subscribe, history, forget };
}
