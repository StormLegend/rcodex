import assert from "node:assert/strict";
import test from "node:test";
import { aggregateUsage, periodBounds } from "../src/usage.mjs";

test("usage periods use local calendar boundaries", () => {
  const now = new Date("2026-09-15T04:00:00.000Z");
  const today = periodBounds("today", now, "Asia/Shanghai");
  assert.equal(today.from.toISOString(), "2026-09-14T16:00:00.000Z");
  assert.equal(today.to.toISOString(), "2026-09-15T16:00:00.000Z");
  const lastMonth = periodBounds("last-month", now, "Asia/Shanghai");
  assert.equal(lastMonth.from.toISOString(), "2026-07-31T16:00:00.000Z");
  assert.equal(lastMonth.to.toISOString(), "2026-08-31T16:00:00.000Z");
});

test("usage aggregation subtracts the pre-period cumulative baseline", () => {
  const sessions = [{ id: "s1", title: "demo", modelProvider: "deepseek", modelLabel: "deepseek-flash", usageHistory: [
    { timestamp: "2026-09-14T15:00:00.000Z", usage: { total: { inputTokens: 100, outputTokens: 50, totalTokens: 150 } } },
    { timestamp: "2026-09-14T17:00:00.000Z", usage: { total: { inputTokens: 120, outputTokens: 70, totalTokens: 190 } } },
    { timestamp: "2026-09-15T03:00:00.000Z", usage: { total: { inputTokens: 140, outputTokens: 80, totalTokens: 220 } } },
  ] }];
  const summary = aggregateUsage(sessions, { range: "today", now: new Date("2026-09-15T04:00:00.000Z"), timezone: "Asia/Shanghai" });
  assert.deepEqual(summary.totals, { inputTokens: 40, outputTokens: 30, cachedInputTokens: 0, reasoningTokens: 0, totalTokens: 70 });
  assert.equal(summary.sessions[0].usage.totalTokens, 70);
});
