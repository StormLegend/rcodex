const DEFAULT_BUFFER = 500;

export function createEventBus({ bufferSize = DEFAULT_BUFFER } = {}) {
  const buffers = new Map();
  const subscribers = new Map();

  function bufferFor(sessionId) {
    if (!buffers.has(sessionId)) buffers.set(sessionId, []);
    return buffers.get(sessionId);
  }

  function emit(sessionId, message) {
    const entry = { ...message, sessionId, timestamp: message.timestamp ?? new Date().toISOString() };
    const buffer = bufferFor(sessionId);
    buffer.push(entry);
    if (buffer.length > bufferSize) buffer.splice(0, buffer.length - bufferSize);
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
    const buffer = bufferFor(sessionId);
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
  }

  return { emit, subscribe, history, forget };
}
