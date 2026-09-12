import crypto from "node:crypto";

export function nowIso() {
  return new Date().toISOString();
}

export function newId() {
  return crypto.randomUUID();
}

export function timingSafeEqual(a, b) {
  const left = Buffer.from(String(a ?? ""), "utf8");
  const right = Buffer.from(String(b ?? ""), "utf8");
  if (left.length !== right.length) return false;
  return crypto.timingSafeEqual(left, right);
}

export function asString(value) {
  return typeof value === "string" ? value : "";
}

export function trimmed(value) {
  return asString(value).trim();
}

export function isRecord(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export async function readJsonBody(req, { limitBytes = 1024 * 1024 } = {}) {
  const chunks = [];
  let size = 0;
  for await (const chunk of req) {
    size += chunk.length;
    if (size > limitBytes) {
      const error = new Error("request body too large");
      error.statusCode = 413;
      throw error;
    }
    chunks.push(chunk);
  }
  if (!chunks.length) return {};
  const text = Buffer.concat(chunks).toString("utf8");
  try {
    return JSON.parse(text);
  } catch {
    const error = new Error("invalid JSON body");
    error.statusCode = 400;
    throw error;
  }
}

export function sendJson(res, statusCode, payload) {
  const body = JSON.stringify(payload);
  res.writeHead(statusCode, {
    "Content-Type": "application/json; charset=utf-8",
    "Content-Length": Buffer.byteLength(body),
    "Cache-Control": "no-store",
  });
  res.end(body);
}

export function sendError(res, statusCode, message, code) {
  sendJson(res, statusCode, { error: code ?? "error", message });
}

export function logError(logger, message, error) {
  logger.error(message, error instanceof Error ? error.stack ?? error.message : String(error));
}
