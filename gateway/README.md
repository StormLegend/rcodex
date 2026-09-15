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

首次使用推荐直接运行自动配置向导：

```bash
node src/cli.mjs setup --yes --install-service
node src/cli.mjs service start
```

已安装用户可执行 `node src/cli.mjs config` 查看脱敏后的生效配置，或用
`node src/cli.mjs service status` 检查后台服务。

向导会读取 `~/.codex/config.toml`，发现 Provider、默认模型和可用的 `env_key`，尝试从各 Provider
的 `/models` 接口获取模型目录，生成 `~/.rcodex/gateway/gateway.env`，并创建用户级 systemd 服务。
API key 只由 Codex 的环境变量读取，不会写入 Gateway 配置或返回给浏览器。没有网络时可加
`--no-network`，向导会使用 Codex 配置里的默认模型继续生成。自定义输出路径使用
`--gateway-env-file`（不要使用 Node.js 自带的 `--env-file` 参数）。

配置也可以放在 env 文件里（`RCODEX_GATEWAY_ENV=/path/to/gateway.env`），变量名与官方网关一致：
`GATEWAY_HOST`、`GATEWAY_PORT`、`GATEWAY_NAME`、`GATEWAY_DATA_DIR`、`GATEWAY_ALLOWED_PATHS`、
`GATEWAY_AUTH_USERNAME`、`GATEWAY_AUTH_PASSWORD`、`GATEWAY_AUTH_TOKEN`、`CODEX_COMMAND`、
`CODEX_AVAILABLE_MODELS`、`CODEX_DEFAULT_MODEL_PROVIDER`、`CODEX_MODEL_PROVIDERS`、
`CODEX_APP_SERVER_STARTUP_TIMEOUT_MS`。
凭据为空时进程拒绝启动（fail closed）。

`CODEX_MODEL_PROVIDERS` 使用 JSON 配置会话可选 Provider 和模型，例如：

```bash
export CODEX_DEFAULT_MODEL_PROVIDER=deepseek
export CODEX_MODEL_PROVIDERS='{"deepseek":["deepseek-flash","deepseek-v4-pro"],"custom":["gpt-5.6-sol"]}'
```

Provider 在 `thread/start` / `thread/resume` 时绑定到会话；同一会话可以继续切换模型，
但不能直接切换 Provider。跨 Provider 请创建新会话。

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
| POST | `/sessions/:id/steer` | 向当前活动轮次追加指令 |
| POST | `/sessions/:id/fork` | 从当前 thread 创建分支会话 |
| POST | `/sessions/:id/resume` | 恢复持久化的 Codex thread |
| PUT | `/sessions/:id/runtime-config` | 修改会话模型、推理强度和 service tier |
| POST | `/sessions/:id/attachments` | 上传 base64 附件到会话工作区 |
| GET | `/sessions/:id/attachments` | 列出会话附件 |
| GET | `/sessions/:id/attachments/:attachmentId` | 下载附件；图片可用 `?inline=1` 内联预览 |
| POST | `/sessions/:id/interrupt` | 打断当前轮次 |
| GET | `/sessions/:id/requests` | 当前等待人工处理的审批 / 提问 |
| POST | `/sessions/:id/approvals/:requestId` | 审批回执 `{"decision":"approve"｜"deny"}` |
| POST | `/sessions/:id/questions/:requestId` | 回答问题 `{"answers":{"q1":["选项"]}}` |
| GET | `/sessions/:id/events` | SSE 事件流（先补历史，再推增量） |
| GET | `/sessions/:id/changes` | 查看工作区 Git 变更状态 |
| GET | `/sessions/:id/changes/diff` | 查看工作区 Git diff |
| GET | `/filesystem/roots` | 允许访问的根目录 |
| GET | `/filesystem/list?path=` | 列目录 |
| GET | `/filesystem/read?path=` | 读文件（默认上限 1 MiB，超出截断） |
| GET | `/usage` | 查看会话与全局 token 用量 |
| GET/POST | `/schedules` | 查看 / 创建持久化定时任务（once / interval） |
| GET | `/schedule-runs?scheduleId=` | 查看任务执行记录 |
| POST | `/schedules/:id/pause` | 暂停任务 |
| POST | `/schedules/:id/resume` | 恢复任务 |
| POST | `/schedules/:id/run` | 立即执行一次任务 |
| DELETE | `/schedules/:id` | 删除任务 |
| GET | `/importable-threads` | 列出当前 Codex 可导入的历史 thread |
| POST | `/sessions/import` | 按 threadId 导入历史会话 |

`/healthz` 是 `/health` 的兼容别名，`/api/usage/summary` 是 `/usage` 的兼容别名。
事件历史会持久化到 `GATEWAY_DATA_DIR/events/`，SSE 可用 `?limit=200` 控制首批回放数量。

除 `/health`、`/console`、`/auth/*` 外都需要 `Authorization: Bearer <token>`；SSE 因为
EventSource 不能带 header，支持 `?token=`。

活跃会话会在网关启动后尝试通过 `thread/resume` 自动恢复；也可以调用
`POST /sessions/:id/resume` 手动恢复。追加轮次时传入
`{"prompt":"...","attachmentIds":["..."]}` 即可选择已上传附件。

## 权限模式（三档，与 rCodex 一致）

新建会话时用 `permissionMode` 指定，默认取 `GATEWAY_PERMISSION_MODE`（默认 `full`）：

| 模式 | 中文 | Codex 侧设置 | 行为 |
| --- | --- | --- | --- |
| `ask` | 请求批准 | `approvalPolicy=on-request`、`approvalsReviewer=user`、沙箱 `workspace-write`（仅工作区可写） | 越权操作会发 `requestApproval`，网关把人挡在 `waiting-approval`，控制台出现**批准/拒绝**按钮，回执后继续 |
| `auto` | 帮我批准 | `approvalPolicy=on-request`、`approvalsReviewer=auto_review`、同样沙箱 | 由 Codex 自己的 reviewer 批准，网关不打扰人（会记一条 `session-approval-auto` 事件） |
| `full` | 完全访问 | `approvalPolicy=never`、沙箱 `danger-full-access` | 不限制、不询问 |

另外，`item/tool/requestUserInput`（agent 向人**提问**，例如"写哪个文件？"）在三档里都可能出现，
网关会把它渲染成提问卡片并通过 `/sessions/:id/questions/:requestId` 回执；它跟权限审批是两回事。

> 实测：三个模式都拿真实 `codex app-server` 跑通过一轮（见下方冒烟命令，用
> `SMOKE_PERMISSION_MODE=ask|auto|full` 切换）。

## 与 Codex 的对接

`src/codex-app-server.mjs` 是一个只依赖 Node 内置模块的 JSON-RPC 客户端：

- 帧格式：stdin/stdout 上的换行分隔 JSON
- 请求：`initialize` → `thread/start` / `thread/resume` → `turn/start`（另有 `turn/interrupt`、`thread/settings/update`）
- 通知：`thread/status/changed`、`item/agentMessage/delta`、`item/completed`、`turn/completed`、
  `turn/plan/updated`、`item/commandExecution/outputDelta` 等，原样透传给控制台/App
- `thread/start` 的 `sandbox` 是字符串（`danger-full-access`），而 `turn/start` 的
  `sandboxPolicy` 是内部标记枚举（`{type:"dangerFullAccess"}`）——两者的形状不一样，踩过坑
- 服务端反向请求：审批类请求按权限模式分流（`ask` 转人工、`auto`/`full` 直接接受），
  `item/tool/requestUserInput` 转人工提问；无人应答时超时后按安全缺省处理（审批=拒绝、提问=空答案）

## 安全模型

- 登录：用户名 + 密码 + 一次性算术验证码；验证码校验后立即作废
- API：静态 bearer token（常量时间比较）；SSE 允许 `?token=`
- 文件：所有路径先 `realpath` 再校验是否落在 `GATEWAY_ALLOWED_PATHS` 内，符号链接不能越界
- Codex 以什么权限跑，取决于 `CODEX_COMMAND` 指向的包装脚本（本仓库附带示例）

## 测试

```bash
npm test                                    # 26 个用例：配置、鉴权、文件、端到端会话流程
RCODEX_SMOKE=1 CODEX_COMMAND=/path/to/rcodex-codex \
  node test/smoke-live.mjs /tmp/workspace "只回复两个字：收到"   # 实弹冒烟（需要真实 Codex）
```

单元测试用一个假 `codex app-server`（`test/fixtures/`）覆盖完整会话流程，不依赖网络。
实弹冒烟会真的拉起 `codex app-server` 并等待模型回复。

## 关于 clean-room

本实现按"协议 + 行为"重写：JSON-RPC 方法名、参数形状、事件名这些**接口事实**来自对真实
`codex app-server` 的黑盒观察；网关自身的代码结构、状态机、控制台、错误处理都是自己写的。
没有复制官方网关的代码。详见仓库根目录的 `docs/ARCHITECTURE.md` 与 `docs/ROADMAP.md`。
