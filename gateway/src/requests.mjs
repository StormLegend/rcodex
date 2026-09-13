import { newId, nowIso } from "./util.mjs";

/**
 * Pending server→client requests that need a human: approval prompts and
 * agent questions. Each entry holds the promise that unblocks the app-server.
 */
export function createRequestStore({ timeoutMs = 600000, logger } = {}) {
  const pending = new Map();

  function create({ sessionId, kind, summary, payload, backendRequestId }) {
    const id = newId();
    let settle;
    const promise = new Promise((resolve) => {
      settle = resolve;
    });
    const timer = setTimeout(() => {
      if (!pending.has(id)) return;
      pending.delete(id);
      logger?.warn(`request ${kind} ${id} timed out; resolving with the safe default`);
      settle(undefined);
    }, timeoutMs);
    timer.unref?.();
    pending.set(id, { id, sessionId, kind, summary, payload, backendRequestId, createdAt: nowIso(), settle, timer, promise });
    return pending.get(id);
  }

  function list(sessionId) {
    return [...pending.values()]
      .filter((entry) => entry.sessionId === sessionId)
      .map(({ id, kind, summary, payload, createdAt }) => ({ id, kind, summary, payload, createdAt }));
  }

  function get(id) {
    return pending.get(id);
  }

  async function resolve(id, value) {
    const entry = pending.get(id);
    if (!entry) return undefined;
    clearTimeout(entry.timer);
    pending.delete(id);
    entry.settle(value ?? {});
    return entry;
  }

  function cancelForSession(sessionId) {
    for (const [id, entry] of pending) {
      if (entry.sessionId !== sessionId) continue;
      clearTimeout(entry.timer);
      pending.delete(id);
      entry.settle(undefined);
    }
  }

  return { create, list, get, resolve, cancelForSession, get size() { return pending.size; } };
}
