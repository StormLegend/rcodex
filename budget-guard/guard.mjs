#!/usr/bin/env node
// rCodex budget guard — a tiny local reverse proxy in front of the DeepSeek API.
//
// DeepSeek keys have no per-key spending limit (the balance is account-wide), so
// this guard meters the traffic passing through it and enforces the budget itself.
// Because it sits in front of *this* machine's Codex, accounting covers this
// machine only — other machines sharing the account do not count.
//
// Costing uses the official price table (USD per 1M tokens, peak/off-peak, cache
// hit vs miss) converted to CNY with GUARD_USD_CNY. Peak hours are 01:00-04:00 and
// 06:00-10:00 UTC, Monday-Friday; everything else is off-peak (half price).
//
// Enforcement (each disabled by 0):
//   GUARD_TOTAL_CNY  refuse once the metered total reaches this amount
//   GUARD_DAILY_CNY  refuse once today's metered spend reaches this amount
//   GUARD_FLOOR_CNY  refuse once the account balance drops below this amount
//
// The caller's Authorization header is forwarded upstream untouched, so rotating
// the API key only requires editing the Codex config, not this service.

import http from "node:http";
import https from "node:https";
import fs from "node:fs";
import path from "node:path";

const PORT = Number(process.env.GUARD_PORT || 8788);
const BIND = process.env.GUARD_BIND || "127.0.0.1";
const UPSTREAM = (process.env.GUARD_UPSTREAM || "https://api.deepseek.com").replace(/\/+$/, "");
const TOTAL_LIMIT_CNY = Number(process.env.GUARD_TOTAL_CNY || 0);
const DAILY_LIMIT_CNY = Number(process.env.GUARD_DAILY_CNY || 0);
const FLOOR_CNY = Number(process.env.GUARD_FLOOR_CNY || 0);
const USD_CNY = Number(process.env.GUARD_USD_CNY || 7.1);
const STATE_FILE = process.env.GUARD_STATE_FILE || "/home/appuser/rcodex-guard/state.json";
const USAGE_LOG = process.env.GUARD_USAGE_LOG || "/home/appuser/rcodex-guard/usage.jsonl";
const BALANCE_TTL_MS = Number(process.env.GUARD_BALANCE_TTL_MS || 60000);
const UPSTREAM_TIMEOUT_MS = Number(process.env.GUARD_UPSTREAM_TIMEOUT_MS || 900000);
const MAX_CAPTURE_BYTES = Number(process.env.GUARD_MAX_CAPTURE_BYTES || 4 * 1024 * 1024);

// USD per 1M tokens at PEAK; off-peak is half. Source: api-docs.deepseek.com pricing.
const PRICE_TABLE = {
  "deepseek-flash": { hit: 0.006, miss: 0.30, out: 1.20 },
  "deepseek-v4-pro": { hit: 0.044, miss: 1.32, out: 3.96 },
};

const upstreamUrl = new URL(UPSTREAM);
const upstreamClient = upstreamUrl.protocol === "http:" ? http : https;

let balanceCache = { at: 0, value: null };
let state = readState();

function log(...args) {
  console.log(new Date().toISOString(), ...args);
}

function today() {
  const d = new Date();
  const pad = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

function isPeak(when = new Date()) {
  const day = when.getUTCDay();
  if (day === 0 || day === 6) return false;
  const hour = when.getUTCHours();
  return (hour >= 1 && hour < 4) || (hour >= 6 && hour < 10);
}

function priceFor(model) {
  const name = String(model || "").toLowerCase();
  return name.includes("pro") ? PRICE_TABLE["deepseek-v4-pro"] : PRICE_TABLE["deepseek-flash"];
}

function readState() {
  try {
    return JSON.parse(fs.readFileSync(STATE_FILE, "utf8"));
  } catch {
    return {};
  }
}

function writeState(next) {
  state = next;
  try {
    fs.mkdirSync(path.dirname(STATE_FILE), { recursive: true });
    fs.writeFileSync(STATE_FILE, JSON.stringify(next, null, 2));
  } catch (err) {
    log("WARN state-write-failed", String(err));
  }
}

function appendUsage(entry) {
  try {
    fs.mkdirSync(path.dirname(USAGE_LOG), { recursive: true });
    fs.appendFileSync(USAGE_LOG, `${JSON.stringify(entry)}\n`);
  } catch (err) {
    log("WARN usage-log-failed", String(err));
  }
}

function readBalance(authHeader) {
  const now = Date.now();
  if (balanceCache.value !== null && now - balanceCache.at < BALANCE_TTL_MS) {
    return Promise.resolve(balanceCache.value);
  }
  return new Promise((resolve, reject) => {
    const req = upstreamClient.request(
      `${UPSTREAM}/user/balance`,
      { method: "GET", headers: authHeader ? { Authorization: authHeader } : {}, timeout: 20000 },
      (res) => {
        let body = "";
        res.setEncoding("utf8");
        res.on("data", (chunk) => { body += chunk; });
        res.on("end", () => {
          try {
            const parsed = JSON.parse(body);
            const cny = (parsed.balance_infos || []).find((info) => info.currency === "CNY");
            const value = cny ? Number(cny.total_balance) : NaN;
            if (!Number.isFinite(value)) throw new Error(`no CNY balance (HTTP ${res.statusCode})`);
            balanceCache = { at: Date.now(), value };
            resolve(value);
          } catch (err) {
            reject(err);
          }
        });
      },
    );
    req.on("timeout", () => req.destroy(new Error("balance request timed out")));
    req.on("error", reject);
    req.end();
  });
}

function deny(res, status, message) {
  const body = JSON.stringify({
    error: { message, type: "insufficient_quota", code: "rcodex_budget_guard" },
  });
  res.writeHead(status, { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(body) });
  res.end(body);
  log("BLOCKED", message);
}

function extractUsage(bodyText, contentType) {
  const pick = (obj) => {
    if (!obj || typeof obj !== "object") return null;
    const holder = obj.response && typeof obj.response === "object" ? obj.response : obj;
    const usage = holder.usage;
    if (!usage) return null;
    const input = usage.input_tokens ?? usage.prompt_tokens;
    const output = usage.output_tokens ?? usage.completion_tokens;
    if (typeof input !== "number" && typeof output !== "number") return null;
    const cached = usage.input_tokens_details?.cached_tokens
      ?? usage.prompt_cache_hit_tokens
      ?? 0;
    return {
      model: holder.model || obj.model || "",
      input: Number(input || 0),
      cached: Number(cached || 0),
      output: Number(output || 0),
    };
  };

  if ((contentType || "").includes("event-stream")) {
    let found = null;
    for (const rawLine of bodyText.split(/\r?\n/)) {
      const line = rawLine.trim();
      if (!line.startsWith("data:")) continue;
      const payload = line.slice(5).trim();
      if (!payload || payload === "[DONE]") continue;
      try {
        const usage = pick(JSON.parse(payload));
        if (usage) found = usage;
      } catch {
        /* partial or non-JSON line */
      }
    }
    return found;
  }
  try {
    return pick(JSON.parse(bodyText));
  } catch {
    return null;
  }
}

function recordUsage(usage) {
  const when = new Date();
  const peak = isPeak(when);
  const price = priceFor(usage.model);
  const factor = peak ? 1 : 0.5;
  const miss = Math.max(0, usage.input - usage.cached);
  const usd = (usage.cached / 1e6) * price.hit * factor
    + (miss / 1e6) * price.miss * factor
    + (usage.output / 1e6) * price.out * factor;
  const cny = usd * USD_CNY;

  const day = today();
  const next = { ...state };
  if (next.spentDay !== day) {
    next.spentDay = day;
    next.spentTodayCny = 0;
  }
  next.spentTotalCny = Number(((next.spentTotalCny || 0) + cny).toFixed(6));
  next.spentTodayCny = Number(((next.spentTodayCny || 0) + cny).toFixed(6));
  next.requests = (next.requests || 0) + 1;
  next.inputTokens = (next.inputTokens || 0) + usage.input;
  next.cachedTokens = (next.cachedTokens || 0) + usage.cached;
  next.outputTokens = (next.outputTokens || 0) + usage.output;
  next.byModel = next.byModel || {};
  const key = usage.model || "unknown";
  const model = next.byModel[key] || { requests: 0, input: 0, cached: 0, output: 0, cny: 0 };
  model.requests += 1;
  model.input += usage.input;
  model.cached += usage.cached;
  model.output += usage.output;
  model.cny = Number((model.cny + cny).toFixed(6));
  next.byModel[key] = model;
  next.lastUsageAt = when.toISOString();
  writeState(next);

  appendUsage({
    at: when.toISOString(),
    model: usage.model,
    peak,
    inputTokens: usage.input,
    cachedInputTokens: usage.cached,
    outputTokens: usage.output,
    costUsd: Number(usd.toFixed(8)),
    costCny: Number(cny.toFixed(6)),
    spentTotalCny: next.spentTotalCny,
  });
  log("usage", usage.model, `in=${usage.input}(cache ${usage.cached}) out=${usage.output}`,
    `${peak ? "peak" : "off-peak"} ¥${cny.toFixed(4)}`, `total ¥${next.spentTotalCny.toFixed(4)}`);
}

async function authorize(req, res) {
  if (TOTAL_LIMIT_CNY > 0 && (state.spentTotalCny || 0) >= TOTAL_LIMIT_CNY) {
    deny(res, 429, `rCodex 预算闸：累计已用 ¥${(state.spentTotalCny || 0).toFixed(2)}，达到总量上限 ¥${TOTAL_LIMIT_CNY.toFixed(2)}，已拒绝本次请求。`);
    return false;
  }
  if (DAILY_LIMIT_CNY > 0 && state.spentDay === today() && (state.spentTodayCny || 0) >= DAILY_LIMIT_CNY) {
    deny(res, 429, `rCodex 预算闸：今天已用 ¥${(state.spentTodayCny || 0).toFixed(2)}，达到每日上限 ¥${DAILY_LIMIT_CNY.toFixed(2)}，已拒绝本次请求。`);
    return false;
  }
  if (FLOOR_CNY > 0) {
    try {
      const balance = await readBalance(req.headers.authorization);
      writeState({ ...state, lastBalance: balance, lastBalanceAt: new Date().toISOString() });
      if (balance < FLOOR_CNY) {
        deny(res, 429, `rCodex 预算闸：账户余额 ¥${balance.toFixed(2)} 低于底线 ¥${FLOOR_CNY.toFixed(2)}，已拒绝本次请求。`);
        return false;
      }
    } catch (err) {
      log("WARN floor check skipped:", String(err));
    }
  }
  return true;
}

function statusPayload() {
  return {
    upstream: UPSTREAM,
    limits: {
      totalCny: TOTAL_LIMIT_CNY || null,
      dailyCny: DAILY_LIMIT_CNY || null,
      floorCny: FLOOR_CNY || null,
    },
    pricing: { unit: "USD per 1M tokens at peak", offPeakFactor: 0.5, usdCny: USD_CNY, table: PRICE_TABLE },
    state,
    now: new Date().toISOString(),
  };
}

const server = http.createServer(async (req, res) => {
  if (req.url === "/__guard/status") {
    const payload = statusPayload();
    try {
      payload.accountBalance = await readBalance(req.headers.authorization);
      if (state.lastBalance !== payload.accountBalance) {
        writeState({ ...state, lastBalance: payload.accountBalance, lastBalanceAt: new Date().toISOString() });
        payload.state = state;
      }
    } catch (err) {
      payload.accountBalanceError = String(err);
    }
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify(payload, null, 2));
    return;
  }

  const started = Date.now();
  const isBalanceProbe = (req.url || "").startsWith("/user/balance");
  if (!isBalanceProbe) {
    const allowed = await authorize(req, res);
    if (!allowed) return;
  }

  const headers = { ...req.headers, host: upstreamUrl.host };
  delete headers["content-length"];

  const upstreamReq = upstreamClient.request(
    `${UPSTREAM}${req.url}`,
    { method: req.method, headers, timeout: UPSTREAM_TIMEOUT_MS },
    (upstreamRes) => {
      res.writeHead(upstreamRes.statusCode || 502, upstreamRes.headers);
      const contentType = upstreamRes.headers["content-type"] || "";
      const parts = [];
      let captured = 0;
      upstreamRes.on("data", (chunk) => {
        if (captured < MAX_CAPTURE_BYTES) {
          parts.push(chunk);
          captured += chunk.length;
        }
      });
      upstreamRes.pipe(res);
      upstreamRes.on("end", () => {
        log("proxy", req.method, req.url, upstreamRes.statusCode, `${Date.now() - started}ms`);
        if ((upstreamRes.statusCode || 500) < 400 && parts.length) {
          const usage = extractUsage(Buffer.concat(parts).toString("utf8"), contentType);
          if (usage) recordUsage(usage);
        }
      });
    },
  );
  upstreamReq.on("timeout", () => upstreamReq.destroy(new Error("upstream timeout")));
  upstreamReq.on("error", (err) => {
    log("ERROR upstream", req.method, req.url, String(err));
    if (!res.headersSent) {
      res.writeHead(502, { "Content-Type": "application/json" });
    }
    res.end(JSON.stringify({ error: { message: `rCodex 预算闸：上游请求失败 ${err.message}` } }));
  });
  req.pipe(upstreamReq);
});

server.listen(PORT, BIND, () => {
  log(`rCodex budget guard listening on http://${BIND}:${PORT} -> ${UPSTREAM}`
    + ` (total ¥${TOTAL_LIMIT_CNY || "off"}, daily ¥${DAILY_LIMIT_CNY || "off"}, floor ¥${FLOOR_CNY || "off"}, usd/cny ${USD_CNY})`);
  log(`metered so far: ¥${(state.spentTotalCny || 0).toFixed(4)} / ${state.requests || 0} request(s)`);
});
