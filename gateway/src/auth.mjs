import crypto from "node:crypto";
import { timingSafeEqual, trimmed } from "./util.mjs";

const CAPTCHA_TTL_MS = 5 * 60 * 1000;
const CAPTCHA_MAX_ENTRIES = 512;

function escapeXml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&apos;");
}

function renderCaptchaSvg(expression) {
  return `<svg xmlns="http://www.w3.org/2000/svg" width="180" height="56" viewBox="0 0 180 56" role="img" aria-label="arithmetic captcha: ${escapeXml(expression)}">
  <rect width="180" height="56" fill="#f8fafc"/>
  <path d="M6 35 C 28 16, 49 47, 74 28 S 122 45, 174 18" fill="none" stroke="#94a3b8" stroke-opacity="0.35" stroke-width="1.6"/>
  <text x="90" y="38" fill="#0f172a" font-size="24" font-weight="700" font-family="ui-monospace, Menlo, Consolas, monospace" text-anchor="middle">${escapeXml(expression)}</text>
</svg>`;
}

export function createCaptchaStore({ now = () => Date.now() } = {}) {
  const challenges = new Map();

  function cleanup() {
    const current = now();
    for (const [id, entry] of challenges) {
      if (entry.expiresAt <= current) challenges.delete(id);
    }
    if (challenges.size <= CAPTCHA_MAX_ENTRIES) return;
    const sorted = [...challenges.entries()].sort((a, b) => a[1].expiresAt - b[1].expiresAt);
    for (const [id] of sorted.slice(0, challenges.size - CAPTCHA_MAX_ENTRIES)) challenges.delete(id);
  }

  function createChallenge() {
    cleanup();
    const operator = crypto.randomInt(2) === 0 ? "+" : "-";
    const left = crypto.randomInt(10);
    const right = crypto.randomInt(10);
    const answer = operator === "+" ? left + right : left - right;
    const expression = `${left} ${operator} ${right} = ?`;
    const captchaId = crypto.randomUUID();
    challenges.set(captchaId, { answer: String(answer), expiresAt: now() + CAPTCHA_TTL_MS });
    return {
      captchaId,
      imageSvg: renderCaptchaSvg(expression),
      expression,
      expiresInSeconds: Math.round(CAPTCHA_TTL_MS / 1000),
    };
  }

  function verify(captchaId, answer) {
    cleanup();
    const entry = challenges.get(trimmed(captchaId));
    if (!entry) return false;
    challenges.delete(captchaId);
    if (entry.expiresAt <= now()) return false;
    return timingSafeEqual(entry.answer, trimmed(answer));
  }

  return { createChallenge, verify };
}

export function validateCredentials(config, username, password) {
  // Credentials are compared exactly (no trimming): a pasted trailing space must
  // fail rather than silently authenticate against a different secret.
  return timingSafeEqual(username, config.authUsername) && timingSafeEqual(password, config.authPassword);
}

export function extractToken(req) {
  const header = req.headers.authorization;
  if (typeof header === "string" && header.trim()) {
    return header.startsWith("Bearer ") ? header.slice(7).trim() : header.trim();
  }
  const host = req.headers.host ?? "127.0.0.1";
  try {
    const url = new URL(req.url ?? "/", `http://${host}`);
    return trimmed(url.searchParams.get("token"));
  } catch {
    return "";
  }
}

export function isAuthorized(req, config) {
  return timingSafeEqual(extractToken(req), config.authToken);
}
