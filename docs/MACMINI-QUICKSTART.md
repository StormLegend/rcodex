# Mac mini 验证入口

本页描述 2026-10-08 部署的独立交互实例。现有服务继续运行。

## 在 Mac mini 上使用

双击桌面 **rcodex 控制台.command**，浏览器会打开
`http://127.0.0.1:18892/console` 并自动读取该实例的访问令牌。
令牌不会写进脚本或终端历史；页面立即清除 URL 片段，只在当前标签页保存登录状态。

1. 新建会话，选择 **Codex**。
2. 默认使用“只读”，可检查文件、分析问题；需要编辑时选择“自动”。
3. 输入任务并发送。可以查看结果、停止任务、确认审批，以及回答助手的问题。
4. 刷新页面或重新选中会话可以继续对话。切换会话不会重复提交任务。

独立工作目录：`~/Applications/rcodex-go-preview/workspace`。
只允许在配置的根目录内创建会话；需要使用其他项目时，先把对应目录加入该实例配置的 `roots`。

Codex 已安装并使用 Mac 现有的 ChatGPT 登录。Claude Code 已安装，但还需要登录。
可以双击桌面 **rcodex Claude 登录.command**，按 Claude 的登录提示完成授权后，在控制台创建 Claude 会话。
“已安装”只代表命令存在，不代表模型服务已授权。

## 从另一台电脑访问

先建立 SSH 隧道（若已有 `macmini` SSH 别名，可以直接替换目标）：

```sh
ssh -N -L 18892:127.0.0.1:18892 sunhao@192.168.49.39
```

然后打开 `http://127.0.0.1:18892/console`。页面需要这个独立实例的访问令牌。
在 Mac mini 的终端执行下列命令可复制令牌到剪贴板，不会在终端输出令牌：

```sh
/usr/bin/python3 "$HOME/Applications/rcodex-go-preview/open-console.py" \
  --config "$HOME/Library/Application Support/rcodex-go-preview/gateway.json" \
  --copy-token
```

当前验证实例只监听本机。手机直接访问 `192.168.49.39:18892` 不会连通；
手机公网入口仍按此前约定暂缓，没有新增或接管 Relay / Cloudflare 公网服务。

## 托管与日志

macOS 使用 launchd 托管，不使用 systemd：

```sh
launchctl print "gui/$(id -u)/com.stormlegend.rcodex-go-preview"
curl -fsS http://127.0.0.1:18892/healthz
```

- 服务：`com.stormlegend.rcodex-go-preview`，登录时启动，进程退出后自动拉起。
- 配置：`~/Library/Application Support/rcodex-go-preview/gateway.json`，权限 `0600`。
- 数据：`~/Library/Application Support/rcodex-go-preview/data`。
- 日志：`~/Library/Logs/rcodex-go-preview/`。
- 二进制：`~/Applications/rcodex-go-preview/rcg`，指向版本目录。
- 出站连接使用 Mac 现有的本机代理 `127.0.0.1:7897`；代理需要保持可用。

`18890`（v0.2.9）和 `18891`（v0.2.10-rc.1）的原有进程、数据、长时间测试均保留。
新实例有自己的令牌、工作目录、数据库和日志。各实例之间不会自动同步或转移任务。
