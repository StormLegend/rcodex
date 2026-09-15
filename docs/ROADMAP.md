# 路线图

## Phase 0 —— 可用骨架（已完成）

- [x] 配置加载（与官方同名的 `GATEWAY_*` / `CODEX_*` 变量，缺凭据拒绝启动）
- [x] 登录：用户名/密码 + 一次性算术验证码；API 用静态 bearer token
- [x] 会话：新建 / 列表 / 详情 / 删除 / 追加轮次 / 打断
- [x] 事件：SSE（历史回放 + 增量 + 保活），原样透传 app-server 通知
- [x] 文件：白名单根目录、`realpath` 防越界、列目录、读文件（带截断）
- [x] 控制台：登录、会话列表、流式输出、发消息
- [x] 测试：28 个单元/集成用例 + 真实 `codex app-server` 实弹冒烟

## Phase 1 —— 达到"日常可用"

> 权限模式按 rCodex 的三档实现：**请求批准（`ask`）/ 帮我批准（`auto`）/ 完全访问（`full`）**。
> `auto` 由 Codex 自己的 reviewer 批准，只有 `ask` 会打扰人；`full` 不限制也不询问。
> 提问（`item/tool/requestUserInput`）三档都可能出现，跟权限审批分开处理。

- [x] 三档权限模式：`ask` / `auto` / `full` → app-server 参数映射，已用真实 Codex 跑通
- [x] 审批回执：`session-approval` 事件 + 控制台批准/拒绝 + `POST /sessions/:id/approvals/:requestId`
- [x] 提问回执：`session-question` 事件 + 控制台选项/输入框 + `POST /sessions/:id/questions/:requestId`
- [x] 会话恢复：网关重启后 `thread/resume` 挂回运行中的线程
- [x] 附件：上传（`/sessions/:id/attachments`），图片输入块和安全工作区存储
- [x] 目录/文件变更视图基础接口：工作区 Git 状态和 diff；事件渲染待增强
- [x] 模型与推理强度切换：`thread/settings/update` + 每会话记忆
- [x] 会话级 Codex Provider/模型选择：`modelProvider` 绑定到 `thread/start` / `thread/resume`
- [x] token 用量：`thread/tokenUsage/updated` 聚合到会话与全局
- [ ] QR 配对：生成 `rcodex://` 连接串给 App 扫码

## Phase 2 —— 编排能力

- [x] 定时任务基础能力（持久化 once / interval、暂停/恢复、立即执行和运行记录）
- [x] 定时任务增强（cron 表达式、Webhook 投递、运行记录）
- [ ] 通知渠道增强（Telegram / 飞书 / 企业微信 / 插件隔离）
- [x] Codex 扩展只读清单（Skills / Plugins / MCP Server / Apps / Hooks）
- [ ] 子 Agent 与线程树（`thread/fork`、subagent 事件聚合）
- [x] 历史导入基础能力（`thread/list`、`thread/import`、`thread/items/list`、`thread/read`）
- [ ] 历史导入增强（异步批量导入、完整分页、远端历史一致性）
- [ ] 多 profile（一套进程托管多个 `HERMES_HOME` 式的隔离环境）

## Phase 3 —— 分发与生态

- [x] npm/源码模式发布 `rcodex-gateway` + setup/service 一键部署脚本
- [ ] 与官方 App 的兼容性回归测试（对同一套接口跑双实现，比对响应）
- [ ] Claude Code provider 支持
- [ ] 指标与可观测性（请求耗时、轮次时长、模型花费）
- [ ] 发布 APK/IPA/desktop 包托管

## 原则

1. **只按协议写实现**：接口事实（方法名、参数、事件名）来自黑盒观察，代码自己写。
2. **fail closed**：凭据缺失、路径越界、审批无人应答，一律拒绝而不是放行。
3. **可测**：每个新能力先补测试；能上真实 Codex 的走实弹冒烟。
