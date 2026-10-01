import assert from "node:assert/strict";
import test from "node:test";
import { createEventBus } from "../src/events.mjs";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

test("event history pages with stable opaque cursors", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "rcodex-events-"));
  const bus = createEventBus({ dataDir: dir, bufferSize: 10 });
  for (let i = 0; i < 5; i++) bus.emit("s1", { type: "event", payload: { i } });
  const first = bus.historyPage("s1", { limit: 2 });
  assert.deepEqual(first.entries.map((entry) => entry.payload.i), [3, 4]);
  assert.equal(first.nextBefore, "4");
  const previous = bus.historyPage("s1", { limit: 2, before: first.nextBefore });
  assert.deepEqual(previous.entries.map((entry) => entry.payload.i), [1, 2]);
  const next = bus.historyPage("s1", { limit: 2, after: "2" });
  assert.deepEqual(next.entries.map((entry) => entry.payload.i), [2, 3]);
});
