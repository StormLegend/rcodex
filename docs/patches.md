# 补丁说明

`patches/patch-gateway.mjs` 是一个**可重复执行、可校验**的补丁器：它读取已安装的
`@rcodex-lab/gateway` 的编译产物，按固定的锚点做精确替换，并在 `--verify-only` 模式下确认
目标文件已经是打过补丁的状态。任何锚点对不上（上游换了实现）时会明确报错而不是乱改；systemd
钩子里用的是 `--fail-open`，即对不上就放行启动并留下警告。

当前补丁针对上游 `1.4.31` / `1.4.35` / `1.4.37`（见 `patches/upstream.json`），修改的文件：

| 文件 | 修复 |
| --- | --- |
| `dist/index.js` | 原始 workbench 窗口写满后仍能翻看更早历史 |
| `dist/codex-session-manager.js` | 旧 Codex 线程不存在时，保留运行期模型切换 |
| `dist/event-payload-filter.js` | 丢弃空的 reasoning 心跳事件，避免把可见消息挤出历史窗口 |
| `dist/console-assets/console.js` | 控制台刷新模型能力模块（避免浏览器缓存旧模块） |
| `dist/console-assets/console-session-runtime-config.js` | 会话内运行时配置控制器同步刷新 |
| `dist/console-assets/console-utils.js` | 回放旧版 4001 字符完成记录时，保留更长的流式内容 |

被修改的原始文件在补丁前会备份到：

```text
~/.local/state/rcodex-gateway-maintained/backups/<upstream-version>/<timestamp>/
```

## 常用命令

```bash
node patches/patch-gateway.mjs --strict                 # 打补丁（默认目标：全局 npm 包）
node patches/patch-gateway.mjs --verify-only --strict    # 只校验
node patches/install-systemd-hook.mjs                    # 安装/刷新 systemd pre-start 钩子
node patches/install-systemd-hook.mjs --remove           # 卸载钩子
node patches/verify-real-event-replay.mjs                # 用真实事件回放验证消息不再被截断
node --test patches/tests/*.test.mjs                     # 回归测试
```

## 升级流程

```bash
npm install -g @rcodex-lab/gateway@<new-version>
node patches/patch-gateway.mjs --strict
node patches/patch-gateway.mjs --verify-only --strict
systemctl --user restart rcodex-gateway.service
```

新版本改了实现导致锚点失效时，`--strict` 会失败并保留未修改状态；确认新实现后在
`patch-gateway.mjs` 里更新锚点即可。
