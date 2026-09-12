# rCodex Maintained（社区维护版）

一套**维护层**：在不重新分发上游代码的前提下，修复 `@rcodex-lab/gateway` 的若干回归问题，并附带一个按 token 计费的预算闸。

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

## 预算闸（可选）

官方 API key 没有"按 key 限额"（余额是账号级的，见 [docs/budget-guard.md](docs/budget-guard.md)）。`budget-guard/` 里是一个 ~300 行的本地反向代理：它坐在 Codex 与 `api.deepseek.com` 之间，按 **token × 官方价目表** 计算花费，达到上限就直接 429 拒绝。

```bash
sudo cp budget-guard/rcodex-budget-guard.service /etc/systemd/system/
sudo cp budget-guard/guard.mjs /home/<user>/rcodex-guard/guard.mjs
sudo systemctl daemon-reload && sudo systemctl enable --now rcodex-budget-guard
```

然后把 Codex 的 provider `base_url` 指向 `http://127.0.0.1:8788/`。

## 仓库结构

```
patches/       补丁脚本、安装钩子、回归测试、验证脚本
budget-guard/  token 计费预算闸（Node，无第三方依赖）
deploy/        systemd 单元示例（系统级部署，非 root 用户运行）
docs/          补丁说明、DeepSeek 用法、预算闸说明
install.sh     一键安装/升级
```

## 许可与边界

- 本仓库**自己的代码**（补丁脚本、预算闸、安装器、文档）采用 MIT，见 [LICENSE](LICENSE)。
- 上游 `@rcodex-lab/gateway` 是**闭源发布的 npm 包**（package.json 无 `license` 字段，包内无 LICENSE 文件）。因此本仓库不包含、也不重新分发它的任何编译产物；安装时由 npm 从官方源拉取，补丁只在你自己机器上应用。
- 如果上游将来提供了开源许可或授权，我们可以把完整 fork 放出来；在那之前，这个仓库是"官方包 + 我们的补丁"。
