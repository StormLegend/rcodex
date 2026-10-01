# 与本机闭源 rCodex Gateway 的能力对照

本表以本机 `@rcodex-lab/gateway@1.4.37` 的 README、路由和 app-server 调用行为为参照；不复制其实现代码。

| 能力 | 自研网关当前状态 | 替代优先级 | 说明 |
| --- | --- | --- | --- |
| 登录、验证码、Bearer 鉴权 | 已完成 | 必须 | 与现有 App/Console 主链路兼容 |
| 会话创建、继续、删除、打断 | 已完成 | 必须 | Codex thread 生命周期 |
| 会话恢复 | 已完成 | 必须 | `thread/resume` + 启动自动恢复 |
| Provider/模型/推理强度 | 已完成 | 必须 | Provider 绑定 thread；模型和推理强度可运行时调整 |
| SSE 事件流 | 已完成 | 必须 | 最近窗口持久化到 JSONL，支持首批回放 limit |
| thread fork / turn steer | 已完成 | 高 | 分支上下文和活动轮次追加指令 |
| 附件与图片 | 已完成基础版 | 高 | JSON base64 上传、下载/inline 预览、工作区安全存储、图片输入；multipart 待增强 |
| Token 用量 | 已完成基础版 | 高 | 会话和全局累计；价格/账单不属于网关职责 |
| 工作区文件浏览 | 已完成 | 必须 | realpath 白名单校验和读取上限 |
| 工作区 Git 状态/diff | 已完成基础版 | 高 | `/changes`、`/changes/diff`；后续渲染 `turn/diff/updated` 事件 |
| 定时任务 | 已完成基础版 | 中 | 持久化 once/interval、暂停/恢复、立即执行、运行记录 |
| 定时任务 cron/Webhook 投递 | 已完成基础版 | 中 | 支持五字段 cron 和通用 Webhook；失败重试/第三方渠道待增强 |
| 通知渠道（Webhook） | 已完成基础版 | 中 | 核心只依赖 HTTP；iLink/飞书等继续走插件隔离 |
| Plugins/MCP/Skills/Apps/Hooks 只读清单 | 已完成基础版 | 中 | 通过 app-server 读取和刷新状态；安装/启停仍待权限设计 |
| 历史导入与完整分页 | 已完成基础版 | 高 | `thread/items/list` 完整分页、重复游标保护，会话和事件 API 支持游标分页；导入会话默认只读 |
| WebSocket Console 通道 | 已完成基础版 | 中 | `/ws` 支持鉴权、会话过滤、事件回放、ping/pong 和慢客户端断开；官方双向协议兼容仍待测试 |
| QR 配对 | 未完成 | 低 | 先稳定 Bearer + 反向代理部署 |
| Claude Code Runtime | 未完成 | 中 | 应作为独立后端，不伪装成 Codex Provider |
| 更新器、诊断、可观测性 | 部分完成 | 中 | 已有健康、按日/周/月用量和基础指标；自动更新和完整诊断待实现 |
| 自动配置与用户级服务 | 已完成基础版 | 必须 | `setup` 自动读取 Codex 配置、发现模型、生成 env 和 systemd 服务 |

## 替代判断

自研网关已经覆盖“远程创建/继续 Codex 会话”的主链路，并具备 Provider 隔离、恢复、附件、变更查看和基础定时任务。要替代本机闭源版的全部外围能力，仍需要按表格继续实现历史导入/分页、通知插件、MCP/Skills 生命周期和更完整的 App 兼容接口。
