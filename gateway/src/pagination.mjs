export function pageLimit(value, fallback = 100, max = 500) {
  if (value === undefined || value === null || value === "") return fallback;
  const n = Number(value);
  if (!Number.isSafeInteger(n) || n < 1 || n > max) throw invalidCursor("invalid page limit");
  return n;
}
export function invalidCursor(message = "invalid cursor") {
  return Object.assign(new Error(message), { statusCode: 400, code: "invalid_pagination" });
}
export function sequence(value) {
  if (value === undefined || value === null || value === "") return undefined;
  if (!/^\d+$/.test(String(value))) throw invalidCursor();
  const n = Number(value);
  if (!Number.isSafeInteger(n) || n < 0) throw invalidCursor();
  return n;
}
