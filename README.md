# rCodex Maintained（社区维护版）

两件事：

1. **`gateway/` —— 我们自己重写的 rCodex 兼容网关**（clean-room，MIT）。直接驱动
   `codex app-server`，提供控制台 + HTTP API，不再依赖上游那份闭源产物。
2. **`patches/` —— 过渡期的维护层**：给还在用官方网关的人用的补丁集，修复若干回归问题。

> 重写进度见 [docs/ROADMAP.md](docs/ROADMAP.md)，架构说明见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)，
> v0.2/v0.3 设计见 [docs/V0.2-DESIGN.md](docs/V0.2-DESIGN.md)，能力对照见 [docs/PARITY.md](docs/PARITY.md)。
> 自研网关当前已跑通：登录（验证码）→ 建会话 → 真实 `codex app-server` 出答案 → SSE 流式回传 → 文件浏览。
> v0.3 已增加：活跃会话恢复、会话级 Provider/模型选择、运行时推理强度切换、工作区附件、token 用量、分支/steer、事件历史、Git diff、定时任务基础能力和自动安装向导。

现在可以用一条命令生成配置并安装用户级服务：

```bash
cd gateway
node src/cli.mjs setup --yes --install-service
node src/cli.mjs service start
```

向导会自动读取本机 Codex Provider 配置，并尝试发现各 Provider 的模型列表。也可以从仓库根目录直接执行 `./install-open.sh`，它会检查 Node.js、生成配置、创建并启用用户级服务。

```bash
cd gateway && npm test                      # 26/26 通过（含端到端会话流程）
RCODEX_SMOKE=1 CODEX_COMMAND=/path/to/rcodex-codex \
  node test/smoke-live.mjs /tmp/ws "只回复两个字：收到"   # 实弹：需要真实 Codex
```

## 维护层（补丁）

在不重新分发上游代码的前提下，修复 `@rcodex-lab/gateway` 的若干回归问题。

> 起因：上游 1.4.35/1.4.37 的改动让"老会话继续输入"和"加载旧版会话"都出了问题——已完成消息被截断到 4000 字符、旧版 4001 字符的完成记录会把更长的流式内容覆盖掉、控制台在升级后会缓存旧的模型能力模块、空的 reasoning 心跳事件会把可见消息挤出历史窗口。这些补丁就是修这些的。

## 装什么、怎么装

本仓库**不含**上游的编译产物。安装脚本会从 npm 拉取官方包，然后在本地打补丁：

```bash
git clone https://github.com/StormLegend/rcodex.git
cd rcodex
./install.sh                 # 默认安装 @rcodex-lab/gateway@1.4.37 并打补丁
# 指定版本：
# GATEWAY_VERSION=1.4.37 ./install.sh
```

脚本做三件事：

1. `npm install -g @rcodex-lab/gateway@<version>`（需要 Node.js >= 22）
2. 应用补丁并**严格校验**（`patch-gateway.mjs --strict` → `--verify-only --strict`），补丁前会自动备份被改文件
3. 安装 systemd 用户级 pre-start 钩子：以后 `npm upgrade` 覆盖了文件，网关启动时会自动重新打补丁

手工执行同样可以：

```bash
node patches/patch-gateway.mjs --target "$(npm root -g)/@rcodex-lab/gateway" --strict
node patches/patch-gateway.mjs --target "$(npm root -g)/@rcodex-lab/gateway" --verify-only --strict
node patches/install-systemd-hook.mjs        # 安装/刷新 systemd 钩子；--remove 卸载
```

## 补丁修了什么

细节见 [docs/patches.md](docs/patches.md)，摘要：

| 问题 | 结果 |
| --- | --- |
| 上游把已完成的 assistant 消息截断到 4000 字符 | 保留完整文本 |
| 1.4.35 的兼容路径会用旧的 4001 字符完成记录覆盖更长的流式视图 | 保留更长的那份 |
| 控制台升级后仍使用缓存的旧模型能力模块 | 控制台入口 / 模型能力 / 运行时配置模块一起改版本号 |
| 空 reasoning 心跳事件挤占历史窗口 | 从历史窗口里丢弃空心跳 |

## 仓库结构

```
patches/       补丁脚本、安装钩子、回归测试、验证脚本
deploy/        systemd 单元示例（系统级部署，非 root 用户运行）
docs/          补丁说明、DeepSeek 用法
install.sh     一键安装/升级
```

## 许可与边界

- 本仓库**自己的代码**（自研网关、补丁脚本、安装器、文档）采用 MIT，见 [LICENSE](LICENSE)。
- 上游 `@rcodex-lab/gateway` 是**闭源发布的 npm 包**（package.json 无 `license` 字段，包内无 LICENSE 文件）。因此本仓库不包含、也不重新分发它的任何编译产物；安装时由 npm 从官方源拉取，补丁只在你自己机器上应用。
- 如果上游将来提供了开源许可或授权，我们可以把完整 fork 放出来；在那之前，这个仓库是"官方包 + 我们的补丁"。
