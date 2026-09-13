# 路线图

## Phase 0 —— 可用骨架（已完成）

- [x] 配置加载（与官方同名的 `GATEWAY_*` / `CODEX_*` 变量，缺凭据拒绝启动）
- [x] 登录：用户名/密码 + 一次性算术验证码；API 用静态 bearer token
- [x] 会话：新建 / 列表 / 详情 / 删除 / 追加轮次 / 打断
- [x] 事件：SSE（历史回放 + 增量 + 保活），原样透传 app-server 通知
- [x] 文件：白名单根目录、`realpath` 防越界、列目录、读文件（带截断）
- [x] 控制台：登录、会话列表、流式输出、发消息
- [x] 测试：10 个单元/集成用例 + 真实 `codex app-server` 实弹冒烟

## Phase 1 —— 达到"日常可用"

> 关于"审批"：这是**运行模式**相关的能力，不是网关必需品。本机（以及 qn）的 Codex 包装脚本
> 用的是 `--dangerously-bypass-approvals-and-sandbox` + `approval_policy="never"` +
> `sandbox_mode="danger-full-access"`，app-server 根本不会发出 `requestApproval`。个人自用网关
> 保持全权模式即可，不需要审批 UI；只有当你想把 Codex 切到受限模式（`workspace-write` +
> `on-request`，也就是 Codex 的原生默认）时才需要有人回执。

- [ ] `item/tool/requestUserInput` 回执：这不是权限审批，而是 agent 执行中向人提问
      （"覆盖哪个文件？""分支叫什么？"）。任何模式下都可能出现，无人应答时 agent 只能自己猜，
      个人网关更需要它
- [ ] （可选）审批 UI：`item/commandExecution/requestApproval`、`applyPatchApproval`、
      `item/permissions/requestApproval`，仅在切到受限模式时才需要；
      全权模式下保持"直接拒绝 + 记日志"即可
- [ ] 会话恢复：网关重启后 `thread/resume` 挂回运行中的线程
- [ ] 附件：上传（`/sessions/:id/attachments`）、下载、图片 inline 展示
- [ ] 目录/文件变更视图：`turn/diff/updated` 渲染为可读 diff
- [ ] 模型与推理强度切换：`thread/settings/update` + 每会话记忆
- [ ] token 用量：`thread/tokenUsage/updated` 聚合到会话与全局
- [ ] QR 配对：生成 `rcodex://` 连接串给 App 扫码

## Phase 2 —— 编排能力

- [ ] 定时任务（cron 表达式、投递目标、失败重试）
- [ ] 通知渠道（webhook / Telegram / 飞书 / 企业微信）
- [ ] 子 Agent 与线程树（`thread/fork`、subagent 事件聚合）
- [ ] 历史导入（`thread/import`、`thread/items/list`、`thread/read`）
- [ ] 多 profile（一套进程托管多个 `HERMES_HOME` 式的隔离环境）

## Phase 3 —— 分发与生态

- [ ] npm 发布 `rcodex-gateway` + `install.sh` 一键部署脚本
- [ ] 与官方 App 的兼容性回归测试（对同一套接口跑双实现，比对响应）
- [ ] Claude Code provider 支持
- [ ] 指标与可观测性（请求耗时、轮次时长、模型花费）
- [ ] 发布 APK/IPA/desktop 包托管

## 原则

1. **只按协议写实现**：接口事实（方法名、参数、事件名）来自黑盒观察，代码自己写。
2. **fail closed**：凭据缺失、路径越界、审批无人应答，一律拒绝而不是放行。
3. **可测**：每个新能力先补测试；能上真实 Codex 的走实弹冒烟。
