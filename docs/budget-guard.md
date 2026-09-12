# 预算闸（token 计费）

## 为什么需要它

DeepSeek 的 API key **没有按 key 限额**：余额是账号级的，`/v1/keys`、`/user/keys`、
`/v1/quota` 之类的管理接口都不存在（探测返回 404）。一把新 key 只是多一把能花同一笔余额的钥匙。

想限制"某台机器只花多少钱"，只能在自己的链路上计量。`budget-guard/guard.mjs` 就是一个
本地反向代理：Codex → 闸（`127.0.0.1:8788`）→ `api.deepseek.com`。它按每次响应里的 usage
统计 token，乘官方价目表算出花费，达到上限直接返回 429。

因为闸只统计经过它自己的流量，**其它机器共用同一账号也不会算进来**。

## 价目表（USD / 1M tokens，peak）

| 模型 | 输入 cache hit | 输入 cache miss | 输出 |
| --- | --- | --- | --- |
| `deepseek-flash` | 0.006 | 0.30 | 1.20 |
| `deepseek-v4-pro` | 0.044 | 1.32 | 3.96 |

- off-peak 是 peak 的一半；peak = 周一至周五 01:00–04:00、06:00–10:00 UTC
- 人民币额度按 `GUARD_USD_CNY` 折算（默认 7.1）

## 配置（systemd 单元里的环境变量）

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `GUARD_PORT` / `GUARD_BIND` | `8788` / `127.0.0.1` | 监听地址 |
| `GUARD_UPSTREAM` | `https://api.deepseek.com` | 上游 |
| `GUARD_TOTAL_CNY` | `0`（关） | 累计上限，达到即拒绝 |
| `GUARD_DAILY_CNY` | `0`（关） | 每日上限 |
| `GUARD_FLOOR_CNY` | `0`（关） | 账户余额低于此值即拒绝 |
| `GUARD_USD_CNY` | `7.1` | 汇率 |
| `GUARD_STATE_FILE` / `GUARD_USAGE_LOG` | `~/rcodex-guard/{state.json,usage.jsonl}` | 累计与逐笔明细 |

## 用法

```bash
# 1) 部署
sudo install -d -o "$USER" -g "$USER" "$HOME/rcodex-guard"
install -m 755 budget-guard/guard.mjs "$HOME/rcodex-guard/guard.mjs"
sudo install -m 644 budget-guard/rcodex-budget-guard.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now rcodex-budget-guard

# 2) 让 Codex 走闸：~/.codex/config.toml 里两个 provider 的 base_url
#    [model_providers.custom]
#    base_url = "http://127.0.0.1:8788/"

# 3) 查询
curl -s -H "Authorization: Bearer $DEEPSEEK_KEY" http://127.0.0.1:8788/__guard/status
tail -5 ~/rcodex-guard/usage.jsonl
```

闸对上游 key 是"透明转发"：调用方带什么 `Authorization` 就用什么，所以换 key 只需要改
Codex 的配置，不需要动闸。
