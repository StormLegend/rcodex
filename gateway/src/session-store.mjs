import fs from "node:fs";
import path from "node:path";

export function createSessionStore({ dataDir }) {
  const file = path.join(dataDir, "sessions.json");
  let sessions = new Map();

  function load() {
    try {
      const parsed = JSON.parse(fs.readFileSync(file, "utf8"));
      const list = Array.isArray(parsed) ? parsed : parsed?.sessions ?? [];
      sessions = new Map(list.filter(Boolean).map((session) => [session.id, session]));
    } catch {
      sessions = new Map();
    }
    return sessions.size;
  }

  function save() {
    fs.mkdirSync(dataDir, { recursive: true });
    const tmp = `${file}.tmp-${process.pid}`;
    fs.writeFileSync(tmp, JSON.stringify([...sessions.values()], null, 2));
    fs.renameSync(tmp, file);
  }

  return {
    load,
    list: () => [...sessions.values()].sort((a, b) => String(b.lastUpdatedAt).localeCompare(String(a.lastUpdatedAt))),
    listPage({ limit = 100, before, after } = {}) {
      const all = [...sessions.values()].sort((a, b) => String(b.lastUpdatedAt).localeCompare(String(a.lastUpdatedAt)));
      const size = Math.max(1, Math.min(500, Number(limit) || 100));
      let start;
      let end;
      if (before !== undefined) {
        end = Math.max(0, Math.min(all.length, Number(before)));
        start = Math.max(0, end - size);
      } else {
        start = after === undefined ? 0 : Math.max(0, Math.min(all.length, Number(after) + 1));
        end = Math.min(all.length, start + size);
      }
      if (start > end) start = end;
      const items = all.slice(start, Math.min(end, start + size));
      return {
        sessions: items,
        nextBefore: start > 0 ? String(start) : undefined,
        nextAfter: start + items.length < all.length ? String(start + items.length - 1) : undefined,
        total: all.length,
      };
    },
    get: (id) => sessions.get(id),
    upsert(session) {
      sessions.set(session.id, session);
      save();
      return session;
    },
    update(id, updater) {
      const existing = sessions.get(id);
      if (!existing) return undefined;
      const next = { ...existing, ...updater(existing) };
      sessions.set(id, next);
      save();
      return next;
    },
    remove(id) {
      const removed = sessions.delete(id);
      if (removed) save();
      return removed;
    },
  };
}
