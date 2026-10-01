const TOKEN_KEYS = ["inputTokens", "outputTokens", "cachedInputTokens", "reasoningTokens", "totalTokens"];

function numeric(value) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
}

export function normalizeUsageSnapshot(raw) {
  const source = raw?.total && typeof raw.total === "object" ? raw.total : raw ?? {};
  const snapshot = {};
  for (const key of TOKEN_KEYS) {
    const snake = key.replace(/[A-Z]/g, (letter) => `_${letter.toLowerCase()}`);
    snapshot[key] = numeric(source[key] ?? source[snake] ?? raw?.[key] ?? raw?.[snake]);
  }
  if (snapshot.totalTokens === 0) {
    snapshot.totalTokens = snapshot.inputTokens + snapshot.outputTokens;
  }
  return snapshot;
}

function zonedParts(date, timezone) {
  const parts = new Intl.DateTimeFormat("en-US", { timeZone: timezone, hour12: false, year: "numeric", month: "numeric", day: "numeric", weekday: "short" }).formatToParts(date);
  const values = Object.fromEntries(parts.filter((part) => part.type !== "literal").map((part) => [part.type, part.value]));
  return { year: Number(values.year), month: Number(values.month), day: Number(values.day), weekday: ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"].indexOf(values.weekday) };
}

function zonedMidnight(year, month, day, timezone) {
  const guess = Date.UTC(year, month - 1, day);
  const parts = new Intl.DateTimeFormat("en-US", { timeZone: timezone, timeZoneName: "longOffset" }).formatToParts(new Date(guess));
  const offsetText = parts.find((part) => part.type === "timeZoneName")?.value || "GMT";
  const match = offsetText.match(/GMT([+-])(\d{2}):(\d{2})/);
  const offsetMinutes = match ? (Number(match[2]) * 60 + Number(match[3])) * (match[1] === "+" ? 1 : -1) : 0;
  return new Date(guess - offsetMinutes * 60_000);
}

function addCalendarDays(year, month, day, days) {
  const value = new Date(Date.UTC(year, month - 1, day + days));
  return { year: value.getUTCFullYear(), month: value.getUTCMonth() + 1, day: value.getUTCDate() };
}

export function periodBounds(range = "all", now = new Date(), timezone = "Asia/Shanghai") {
  const normalized = String(range || "all").toLowerCase();
  if (normalized === "all") return { range: "all", from: new Date(0), to: now };
  if (!["today", "day", "week", "month", "last-month", "previous-month"].includes(normalized)) {
    const error = new Error("range must be today, week, month, last-month, or all");
    error.statusCode = 400;
    error.code = "invalid_usage_range";
    throw error;
  }
  const current = zonedParts(now, timezone);
  let start;
  let canonical = normalized === "day" ? "today" : normalized === "previous-month" ? "last-month" : normalized;
  if (canonical === "today") {
    start = { year: current.year, month: current.month, day: current.day };
  } else if (canonical === "week") {
    start = addCalendarDays(current.year, current.month, current.day, -((current.weekday + 6) % 7));
  } else if (canonical === "month") {
    start = { year: current.year, month: current.month, day: 1 };
  } else {
    const previous = new Date(Date.UTC(current.year, current.month - 2, 1));
    start = { year: previous.getUTCFullYear(), month: previous.getUTCMonth() + 1, day: 1 };
  }
  let end;
  if (canonical === "last-month") end = { year: current.year, month: current.month, day: 1 };
  else if (canonical === "month") {
    const next = new Date(Date.UTC(current.year, current.month, 1));
    end = { year: next.getUTCFullYear(), month: next.getUTCMonth() + 1, day: 1 };
  } else {
    const next = addCalendarDays(start.year, start.month, start.day, canonical === "today" ? 1 : 7);
    end = next;
  }
  return { range: canonical, from: zonedMidnight(start.year, start.month, start.day, timezone), to: zonedMidnight(end.year, end.month, end.day, timezone) };
}

export function aggregateUsage(sessions, { range = "all", now = new Date(), timezone = "Asia/Shanghai" } = {}) {
  const bounds = periodBounds(range, now, timezone);
  const totals = Object.fromEntries(TOKEN_KEYS.map((key) => [key, 0]));
  const rows = [];
  for (const session of sessions) {
    const history = (Array.isArray(session.usageHistory) ? session.usageHistory : [])
      .map((entry) => ({ timestamp: new Date(entry.timestamp), usage: normalizeUsageSnapshot(entry.usage) }))
      .filter((entry) => Number.isFinite(entry.timestamp.getTime()))
      .sort((a, b) => a.timestamp - b.timestamp);
    const before = history.filter((entry) => entry.timestamp < bounds.from).at(-1)?.usage ?? Object.fromEntries(TOKEN_KEYS.map((key) => [key, 0]));
    const current = history.filter((entry) => entry.timestamp < bounds.to).at(-1)?.usage ?? before;
    const usage = {};
    for (const key of TOKEN_KEYS) {
      usage[key] = Math.max(0, numeric(current[key]) - numeric(before[key]));
      totals[key] += usage[key];
    }
    rows.push({ id: session.id, title: session.title, modelProvider: session.modelProvider, model: session.modelLabel, usage: { ...usage, total: { ...usage } } });
  }
  return { range: bounds.range, timezone, from: bounds.from.toISOString(), to: bounds.to.toISOString(), sessions: rows, totals };
}
