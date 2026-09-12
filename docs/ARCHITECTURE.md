# 架构说明（自研网关）

## 组件

```
┌─────────────┐   HTTP/SSE   ┌──────────────────┐   JSON-RPC (stdio)   ┌──────────────────┐
│ 控制台 / App │ ───────────▶ │ rcodex-gateway    │ ───────────────────▶ │ codex app-server │
└─────────────┘              │ (src/server.mjs)  │                      └──────────────────┘
                             │  ├─ auth          │                                │
                             │  ├─ sessions      │◀── 通知：item/… turn/… ─────────┘
                             │  ├─ event bus     │
                             │  └─ filesystem    │
                             └──────────────────┘
```

源码划分（`gateway/src/`）：

| 文件 | 职责 |
| --- | --- |
| `cli.mjs` | 入口：`start` / `service run`，信号处理 |
| `config.mjs` | 环境变量与 env 文件解析，缺凭据直接拒绝启动 |
| `auth.mjs` | 验证码（算术 SVG、一次性）、账号密码校验、bearer 校验 |
| `server.mjs` | 路由（公开路由 → 鉴权 → 会话路由 → 文件路由）、统一错误处理 |
| `sessions.mjs` | 会话生命周期：起线程、发轮次、通知翻译、状态机 |
| `codex-app-server.mjs` | `codex app-server` 的 JSON-RPC 客户端（含反向请求处理） |
| `events.mjs` | 每会话事件总线（环形缓冲 + 订阅），支撑 SSE 补齐与增量 |
| `session-store.mjs` | 会话持久化（`dataDir/sessions.json`，原子写） |
| `filesystem.mjs` | `realpath` + 白名单校验后的列目录/读文件 |
| `console.mjs` | 单页控制台（登录、会话列表、SSE 流式输出、发轮次） |

## 会话状态机

```
starting ──▶ running ──▶ completed
                 │
                 ├──▶ failed      (thread/status/changed: systemError, 或启动失败)
                 └──▶ waiting-approval   (Phase 1：接入审批 UI 后)
```

- `createSession`：`thread/start` → 落库（`starting`）→ `turn/start` → `running`
- 通知 `turn/completed` 或 `thread/status/changed(idle)` → `completed`
- 每一轮都记录 `activeTurnId`、`lastTurnStartedAt`、`lastTurnFinishedAt`

## 事件流

网关把 app-server 的通知原样包装成 `session-output` 事件：

```json
{
  "type": "session-output",
  "sessionId": "...",
  "timestamp": "...",
  "payload": {
    "stream": "event",
    "format": "jsonl",
    "eventType": "item/agentMessage/delta",
    "jsonPayload": { "threadId": "...", "delta": "收到" }
  }
}
```

另外派生两个便于直接渲染的事件：`session-message-delta`（纯文本增量）和
`session-status`（状态变化，带最新 session 快照）。SSE 连接建立时先回放缓冲历史，再推增量；
25 秒一次注释帧保活。

## 与官方实现的差异（有意为之）

- `/` 会 302 跳到 `/console`（官方对根路径直接 404）
- 文件读取有 1 MiB 上限并显式返回 `truncated`，避免大文件把内存打满
- 凭据比较不做 trim，粘贴带入的空格不会静默通过
- 反向请求（审批）默认拒绝而不是静默等待

## 兼容性注意

`turn/start` 需要 `sandboxPolicy: { type: "dangerFullAccess" }`，`thread/start` 收字符串
`sandbox: "danger-full-access"`；两者形状不同，是实测出来的（协议里没有文档）。
