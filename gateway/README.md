# rcodex-gateway

自研（clean-room）的 rCodex 兼容网关：直接驱动 `codex app-server`，对外提供控制台和 HTTP API。

```
浏览器 / rCodex App ──HTTP──▶ rcodex-gateway ──JSON-RPC(stdio)──▶ codex app-server ──▶ 模型
```

## 快速开始

```bash
node --version                      # 需要 >= 22
export GATEWAY_AUTH_USERNAME=admin
export GATEWAY_AUTH_PASSWORD='换成你的密码'
export GATEWAY_AUTH_TOKEN="$(openssl rand -hex 24)"
export GATEWAY_ALLOWED_PATHS="$HOME"
export CODEX_COMMAND=/path/to/rcodex-codex     # 指向包装脚本或 codex 本体
export CODEX_AVAILABLE_MODELS="deepseek-flash deepseek-v4-pro"

node src/cli.mjs start
# 控制台 http://127.0.0.1:8787/console
```

配置也可以放在 env 文件里（`RCODEX_GATEWAY_ENV=/path/to/gateway.env`），变量名与官方网关一致：
`GATEWAY_HOST`、`GATEWAY_PORT`、`GATEWAY_NAME`、`GATEWAY_DATA_DIR`、`GATEWAY_ALLOWED_PATHS`、
`GATEWAY_AUTH_USERNAME`、`GATEWAY_AUTH_PASSWORD`、`GATEWAY_AUTH_TOKEN`、`CODEX_COMMAND`、
`CODEX_AVAILABLE_MODELS`、`CODEX_APP_SERVER_STARTUP_TIMEOUT_MS`。
凭据为空时进程拒绝启动（fail closed）。

## 已实现的接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/health` | 健康检查（公开） |
| GET | `/console` | 控制台页面（公开，登录后可操作） |
| GET | `/auth/captcha` | 算术验证码（SVG） |
| POST | `/auth/login` | 用户名/密码/验证码 → token |
| GET | `/sessions` | 会话列表 |
| POST | `/sessions` | 新建会话并发出第一轮（`workspacePath`、`prompt`、`model`、`reasoningEffort`） |
| GET/DELETE | `/sessions/:id` | 会话详情 / 删除 |
| POST | `/sessions/:id/turns` | 追加一轮 |
| POST | `/sessions/:id/interrupt` | 打断当前轮次 |
| GET | `/sessions/:id/events` | SSE 事件流（先补历史，再推增量） |
| GET | `/filesystem/roots` | 允许访问的根目录 |
| GET | `/filesystem/list?path=` | 列目录 |
| GET | `/filesystem/read?path=` | 读文件（默认上限 1 MiB，超出截断） |

除 `/health`、`/console`、`/auth/*` 外都需要 `Authorization: Bearer <token>`；SSE 因为
EventSource 不能带 header，支持 `?token=`。

## 与 Codex 的对接

`src/codex-app-server.mjs` 是一个只依赖 Node 内置模块的 JSON-RPC 客户端：

- 帧格式：stdin/stdout 上的换行分隔 JSON
- 请求：`initialize` → `thread/start` → `turn/start`（另有 `turn/interrupt`）
- 通知：`thread/status/changed`、`item/agentMessage/delta`、`item/completed`、`turn/completed`、
  `turn/plan/updated`、`item/commandExecution/outputDelta` 等，原样透传给控制台/App
- `thread/start` 的 `sandbox` 是字符串（`danger-full-access`），而 `turn/start` 的
  `sandboxPolicy` 是内部标记枚举（`{type:"dangerFullAccess"}`）——两者的形状不一样，踩过坑
- 服务端反向请求：当前版本一律礼貌拒绝并记录日志。**在
  `--dangerously-bypass-approvals-and-sandbox` + `approval_policy="never"` 的全权模式下，
  本机不会收到审批类请求**（这段是保险丝）；`item/tool/requestUserInput`（agent 向人提问）
  在 Phase 1 做，它跟权限审批不是一回事

## 安全模型

- 登录：用户名 + 密码 + 一次性算术验证码；验证码校验后立即作废
- API：静态 bearer token（常量时间比较）；SSE 允许 `?token=`
- 文件：所有路径先 `realpath` 再校验是否落在 `GATEWAY_ALLOWED_PATHS` 内，符号链接不能越界
- Codex 以什么权限跑，取决于 `CODEX_COMMAND` 指向的包装脚本（本仓库附带示例）

## 测试

```bash
npm test                                    # 10 个用例：配置、鉴权、文件、端到端会话流程
RCODEX_SMOKE=1 CODEX_COMMAND=/path/to/rcodex-codex \
  node test/smoke-live.mjs /tmp/workspace "只回复两个字：收到"   # 实弹冒烟（需要真实 Codex）
```

单元测试用一个假 `codex app-server`（`test/fixtures/`）覆盖完整会话流程，不依赖网络。
实弹冒烟会真的拉起 `codex app-server` 并等待模型回复。

## 关于 clean-room

本实现按"协议 + 行为"重写：JSON-RPC 方法名、参数形状、事件名这些**接口事实**来自对真实
`codex app-server` 的黑盒观察；网关自身的代码结构、状态机、控制台、错误处理都是自己写的。
没有复制官方网关的代码。详见仓库根目录的 `docs/ARCHITECTURE.md` 与 `docs/ROADMAP.md`。
