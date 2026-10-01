import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { createSessionStore } from "../src/session-store.mjs";

test("session list pages preserve newest-first cursors", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "rcodex-store-"));
  const store = createSessionStore({ dataDir: dir });
  for (let i = 0; i < 5; i++) store.upsert({ id: `s${i}`, lastUpdatedAt: `2026-09-0${i + 1}T00:00:00.000Z` });
  const first = store.listPage({ limit: 2 });
  assert.deepEqual(first.sessions.map((s) => s.id), ["s4", "s3"]);
  const previous = store.listPage({ limit: 2, after: first.nextAfter });
  assert.deepEqual(previous.sessions.map((s) => s.id), ["s2", "s1"]);
});
