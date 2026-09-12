import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import { newId } from "./util.mjs";

/**
 * Minimal JSON-RPC client for `codex app-server`.
 *
 * Framing: newline-delimited JSON on stdin/stdout.
 *   -> {"jsonrpc":"2.0","id":1,"method":"initialize","params":{...}}
 *   <- {"jsonrpc":"2.0","id":1,"result":{...}}
 *   <- {"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{...}}   (notification)
 *   <- {"jsonrpc":"2.0","id":"srv-1","method":"applyPatchApproval","params":{...}} (server request)
 */
export class CodexAppServer {
  constructor({ command, args = ["app-server"], cwd, env, logger, requestTimeoutMs = 120000 }) {
    this.command = command;
    this.args = args;
    this.cwd = cwd;
    this.env = env ?? process.env;
    this.logger = logger ?? console;
    this.requestTimeoutMs = requestTimeoutMs;
    this.pending = new Map();
    this.listeners = new Set();
    this.serverRequestHandlers = new Set();
    this.child = undefined;
    this.stopping = false;
  }

  onNotification(listener) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  onServerRequest(handler) {
    this.serverRequestHandlers.add(handler);
    return () => this.serverRequestHandlers.delete(handler);
  }

  async start() {
    if (this.child) return;
    const child = spawn(this.command, this.args, {
      cwd: this.cwd,
      env: this.env,
      stdio: ["pipe", "pipe", "pipe"],
    });
    this.child = child;

    const lines = createInterface({ input: child.stdout });
    lines.on("line", (line) => this.#handleLine(line));
    child.stderr.setEncoding("utf8");
    child.stderr.on("data", (chunk) => {
      const text = String(chunk).trim();
      if (text) this.logger.debug?.("codex stderr:", text);
    });
    child.on("exit", (code, signal) => {
      this.child = undefined;
      const error = new Error(`codex app-server exited (code=${code ?? "null"} signal=${signal ?? "null"})`);
      for (const { reject, timer } of this.pending.values()) {
        clearTimeout(timer);
        reject(error);
      }
      this.pending.clear();
      if (!this.stopping) this.logger.warn(error.message);
    });
    child.stdin.on("error", () => {
      /* the exit handler reports the failure */
    });
  }

  #handleLine(line) {
    const text = line.trim();
    if (!text) return;
    let message;
    try {
      message = JSON.parse(text);
    } catch {
      this.logger.debug?.("codex app-server: non-JSON line", text.slice(0, 200));
      return;
    }

    if (message.id !== undefined && (message.result !== undefined || message.error !== undefined)) {
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      clearTimeout(pending.timer);
      if (message.error) pending.reject(new Error(message.error.message ?? JSON.stringify(message.error)));
      else pending.resolve(message.result);
      return;
    }

    if (message.method && message.id !== undefined) {
      this.#handleServerRequest(message);
      return;
    }

    if (message.method) {
      for (const listener of this.listeners) {
        try {
          listener(message);
        } catch (error) {
          this.logger.warn("notification listener failed:", error?.message ?? String(error));
        }
      }
    }
  }

  #handleServerRequest(message) {
    for (const handler of this.serverRequestHandlers) {
      try {
        if (handler(message, this) === true) return;
      } catch (error) {
        this.logger.warn("server request handler failed:", error?.message ?? String(error));
      }
    }
    // No handler claimed it: refuse explicitly so Codex does not wait forever.
    this.respondError(message.id, -32601, `unsupported server request: ${message.method}`);
  }

  send(payload) {
    if (!this.child?.stdin.writable) throw new Error("codex app-server is not running");
    this.child.stdin.write(`${JSON.stringify(payload)}\n`);
  }

  respond(id, result) {
    this.send({ jsonrpc: "2.0", id, result });
  }

  respondError(id, code, message) {
    this.send({ jsonrpc: "2.0", id, error: { code, message } });
  }

  request(method, params = {}, { timeoutMs = this.requestTimeoutMs } = {}) {
    const id = newId();
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`codex app-server request timed out: ${method}`));
      }, timeoutMs);
      timer.unref?.();
      this.pending.set(id, { resolve, reject, timer });
      try {
        this.send({ jsonrpc: "2.0", id, method, params });
      } catch (error) {
        this.pending.delete(id);
        clearTimeout(timer);
        reject(error);
      }
    });
  }

  async initialize() {
    await this.start();
    await this.request("initialize", {
      clientInfo: { name: "rcodex-gateway", version: "0.1.0" },
      capabilities: { experimentalApi: true },
    });
  }

  async startThread(options = {}) {
    const response = await this.request("thread/start", {
      cwd: options.cwd,
      approvalPolicy: options.approvalPolicy,
      approvalsReviewer: options.approvalsReviewer,
      sandbox: options.sandbox,
      model: options.model,
      ephemeral: false,
    });
    return response?.thread ?? response;
  }

  async startTurn({ threadId, prompt, cwd, model, effort, approvalPolicy, approvalsReviewer, sandboxPolicy }) {
    const response = await this.request("turn/start", {
      threadId,
      input: [{ type: "text", text: prompt, text_elements: [] }],
      cwd,
      model,
      effort,
      approvalPolicy,
      approvalsReviewer,
      // turn/start expects the internally tagged enum, not the legacy string.
      sandboxPolicy,
    });
    return response?.turn?.id ?? response?.turnId;
  }

  async interrupt(threadId) {
    await this.request("turn/interrupt", { threadId });
  }

  async stop() {
    this.stopping = true;
    const child = this.child;
    this.child = undefined;
    if (!child) return;
    await new Promise((resolve) => {
      const done = () => resolve();
      child.once("exit", done);
      child.kill("SIGTERM");
      setTimeout(() => {
        child.kill("SIGKILL");
        resolve();
      }, 5000).unref?.();
    });
  }
}
