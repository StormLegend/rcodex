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
