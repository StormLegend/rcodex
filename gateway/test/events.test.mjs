import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createEventBus } from "../src/events.mjs";

test("event history survives a new event bus instance", () => {
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "rcodex-events-"));
  const first = createEventBus({ dataDir, bufferSize: 10 });
  first.emit("session-1", { type: "session-started", payload: { ok: true } });
  first.emit("session-1", { type: "session-message-delta", payload: { text: "hello" } });

  const second = createEventBus({ dataDir, bufferSize: 10 });
  const history = second.history("session-1");
  assert.equal(history.length, 2);
  assert.equal(history[1].payload.text, "hello");
});
