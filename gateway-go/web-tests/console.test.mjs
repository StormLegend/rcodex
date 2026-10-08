import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { chromium } from "playwright";

const html = await readFile(
  new URL("../internal/web/console.html", import.meta.url),
);
const token = "browser-fixture-token-never-a-real-secret";
let browser;
before(async () => {
  browser = await chromium.launch({ headless: true });
});
after(async () => {
  await browser?.close();
});

async function fixture(t, prefix = "") {
  const sessions = [],
    turns = [],
    approvals = [],
    streams = new Map(),
    attempts = [];
  let sequence = 0,
    eventID = 0,
    failOnce = false;
  function emit(id, kind = "turn.updated", data = {}) {
    for (const response of streams.get(id) || [])
      response.write(
        `id: ${++eventID}\ndata: ${JSON.stringify({ id: eventID, kind, data })}\n\n`,
      );
  }
  const server = createServer(async (req, res) => {
    const url = new URL(req.url, "http://localhost");
    const path = url.pathname.slice(prefix.length);
    const send = (data, status = 200) => {
      res.writeHead(status, { "Content-Type": "application/json" });
      res.end(JSON.stringify(data));
    };
    if (!url.pathname.startsWith(prefix)) return send({}, 404);
    if (path === "/console") {
      res.setHeader("Content-Type", "text/html; charset=utf-8");
      return res.end(html);
    }
    if (path === "/favicon.ico") {
      res.writeHead(204);
      return res.end();
    }
    if (req.headers.authorization !== "Bearer " + token)
      return send({ error: "unauthorized" }, 401);
    let body = "";
    for await (const chunk of req) body += chunk;
    const data = body ? JSON.parse(body) : {};
    if (path === "/healthz")
      return send({ ok: true, version: "browser-fixture" });
    if (path === "/api/runtime/status")
      return send({
        runtimes: [
          { id: "codex", name: "Codex", available: true },
          { id: "claude", name: "Claude Code", available: false },
        ],
        workspaces: ["/fixture/workspace"],
        modes: ["readonly", "ask", "auto"],
      });
    if (path === "/api/sessions") {
      if (req.method === "POST") {
        const session = { id: "s" + ++sequence, ...data };
        sessions.unshift(session);
        return send({ session }, 201);
      }
      return send({ sessions, next_before: 0 });
    }
    const history = path.match(/^\/api\/sessions\/([^/]+)\/history$/);
    if (history)
      return send({
        turns: turns.filter((x) => x.session_id === history[1]),
        next_before: 0,
      });
    const stream = path.match(/^\/api\/sessions\/([^/]+)\/events\/stream$/);
    if (stream) {
      res.writeHead(200, {
        "Content-Type": "text/event-stream",
        "Cache-Control": "no-cache",
      });
      res.write(": connected\n\n");
      const group = streams.get(stream[1]) || new Set();
      group.add(res);
      streams.set(stream[1], group);
      res.on("close", () => group.delete(res));
      return;
    }
    if (path === "/api/turns") {
      attempts.push(data);
      if (failOnce) {
        failOnce = false;
        return send({ error: "temporary fixture failure" }, 503);
      }
      const turn = {
        id: "t" + ++sequence,
        session_id: data.session_id,
        prompt: data.prompt,
        state: "running",
        result: "",
      };
      turns.unshift(turn);
      if (data.prompt === "approval" || data.prompt === "question") {
        const question = data.prompt === "question";
        approvals.push({
          id: "a" + sequence,
          session_id: turn.session_id,
          turn_id: turn.id,
          state: "pending",
          request: {
            kind: question ? "question" : "approval",
            method: question
              ? "item/tool/requestUserInput"
              : "item/commandExecution/requestApproval",
            params: question
              ? {
                  questions: [
                    {
                      id: "color",
                      header: "Color",
                      question: "选择一个颜色",
                      options: [{ label: "蓝色", description: "Blue" }],
                    },
                  ],
                }
              : { command: "echo safe-fixture", reason: "需要执行命令" },
          },
        });
      } else if (data.prompt !== "cancel-me") {
        setTimeout(() => {
          turn.state = "completed";
          turn.result = "REPLY: " + data.prompt;
          emit(turn.session_id);
        }, 200).unref();
      }
      emit(turn.session_id);
      return send({ turn }, 202);
    }
    if (path === "/api/approvals")
      return send({
        approvals: approvals.filter((a) => a.state === "pending"),
      });
    const approval = path.match(/^\/api\/approvals\/([^/]+)$/);
    if (approval) {
      const a = approvals.find((a) => a.id === approval[1]);
      a.state = "resolved";
      a.response = data;
      const turn = turns.find((x) => x.id === a.turn_id);
      turn.state = "completed";
      turn.result = JSON.stringify(data);
      emit(turn.session_id);
      return send({ ok: true });
    }
    const cancel = path.match(/^\/api\/turns\/([^/]+)\/cancel$/);
    if (cancel) {
      const turn = turns.find((x) => x.id === cancel[1]);
      turn.state = "cancelled";
      emit(turn.session_id);
      return send({ ok: true });
    }
    return send({ error: "not found" }, 404);
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const base = `http://127.0.0.1:${server.address().port}${prefix}`;
  const context = await browser.newContext();
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  t.after(async () => {
    await context.close();
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
    assert.deepEqual(errors, []);
  });
  async function connect(fragment = false) {
    await page.goto(base + "/console" + (fragment ? "#token=" + token : ""));
    if (!fragment) {
      await page.locator("#token").fill(token);
      await page.locator("#connect").click();
    }
    await page.waitForFunction(
      () => !document.querySelector("#create").disabled,
    );
  }
  async function create(title = "Browser acceptance") {
    await page.locator("#title").fill(title);
    await page.locator("#create").click();
    await page.waitForFunction(() => !document.querySelector("#send").disabled);
  }
  async function prompt(value) {
    await page.locator("#prompt").fill(value);
    await page.locator("#send").click();
  }
  return {
    page,
    connect,
    create,
    prompt,
    sessions,
    turns,
    approvals,
    streams,
    attempts,
    base,
    failNext() {
      failOnce = true;
    },
  };
}

test("auth, safe rendering, send, persisted history, and mobile layout", async (t) => {
  const f = await fixture(t);
  await f.page.goto(f.base + "/console");
  await f.page.locator("#token").fill("wrong");
  await f.page.locator("#connect").click();
  await f.page.locator("#status.error").waitFor();
  assert.equal(await f.page.locator("#create").isDisabled(), true);
  await f.connect(true);
  assert.equal(new URL(f.page.url()).hash, "");
  assert.equal(await f.page.locator("#mode").inputValue(), "readonly");
  assert.equal(
    await f.page
      .locator('#runtime option[value="claude"]')
      .evaluate((option) => option.disabled),
    true,
  );
  await f.create('<img src=x onerror="window.pwned=true">');
  assert.equal(await f.page.locator("#session-title img").count(), 0);
  await f.prompt("hello <script>alert(1)</script>");
  await f.page.waitForFunction(() =>
    document.querySelector("#history").textContent.includes("REPLY: hello"),
  );
  assert.equal(await f.page.evaluate(() => window.pwned), undefined);
  assert.equal(f.sessions[0].workspace, "/fixture/workspace");
  await f.page.reload();
  await f.page.waitForFunction(() =>
    document.querySelector("#history").textContent.includes("REPLY: hello"),
  );
  await f.page.setViewportSize({ width: 390, height: 844 });
  assert.equal(
    await f.page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    true,
  );
  await f.page.locator("#disconnect").click();
  assert.equal(await f.page.evaluate(() => sessionStorage.length), 0);
  assert.equal(await f.page.locator("#send").isDisabled(), true);
});

test("approve, deny, and answer runtime questions", async (t) => {
  const f = await fixture(t);
  await f.connect();
  await f.create();
  await f.prompt("approval");
  await f.page.getByRole("button", { name: "允许", exact: true }).click();
  await f.page.waitForFunction(() =>
    document.querySelector("#history").textContent.includes('"approved":true'),
  );
  assert.equal(f.approvals[0].response.approved, true);
  await f.prompt("approval");
  await f.page.getByRole("button", { name: "拒绝", exact: true }).click();
  await f.page.waitForFunction(() =>
    document.querySelector("#history").textContent.includes('"approved":false'),
  );
  await f.prompt("question");
  await f.page.getByLabel("Color选项").selectOption("蓝色");
  await f.page.getByRole("button", { name: "提交回答", exact: true }).click();
  await f.page.waitForFunction(() =>
    document.querySelector("#history").textContent.includes("蓝色"),
  );
  assert.deepEqual(f.approvals[2].response.answers, {
    color: { answers: ["蓝色"] },
  });
});

test("cancel tasks and close old SSE connections when switching or disconnecting", async (t) => {
  const f = await fixture(t);
  await f.connect();
  await f.create("First");
  const first = f.sessions[0].id;
  await f.prompt("cancel-me");
  await f.page.locator("#cancel").click();
  await f.page.waitForFunction(() =>
    document.querySelector("#history").textContent.includes("已停止"),
  );
  assert.equal(f.turns[0].state, "cancelled");
  await f.create("Second");
  await new Promise((resolve) => setTimeout(resolve, 100));
  assert.equal(f.streams.get(first)?.size || 0, 0);
  await f.page.locator("#disconnect").click();
  await new Promise((resolve) => setTimeout(resolve, 100));
  assert.equal(
    [...f.streams.values()].reduce((n, s) => n + s.size, 0),
    0,
  );
});

test("retry preserves idempotency key and gateway path prefix", async (t) => {
  const f = await fixture(t, "/v1/gateways/macmini");
  await f.connect();
  await f.create();
  f.failNext();
  await f.prompt("retry-safe");
  await f.page.locator("#status.error").waitFor();
  await f.page.waitForFunction(() => !document.querySelector("#send").disabled);
  await f.page.locator("#send").click();
  await f.page.waitForFunction(() =>
    document
      .querySelector("#history")
      .textContent.includes("REPLY: retry-safe"),
  );
  assert.equal(f.attempts.length, 2);
  assert.equal(f.attempts[0].idempotency_key, f.attempts[1].idempotency_key);
  assert.equal(f.turns.length, 1);
});
