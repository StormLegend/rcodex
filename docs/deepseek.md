# 用 DeepSeek 跑 rCodex

## Codex 侧配置（`~/.codex/config.toml`）

```toml
model = "deepseek-flash"
model_provider = "custom"
preferred_auth_method = "apikey"
forced_login_method = "api"
model_reasoning_effort = "high"
model_catalog_json = "~/.codex/models.json"
web_search = "disabled"

[model_providers.custom]
name = "custom"
base_url = "https://api.deepseek.com/"     # 想走预算闸就改成 http://127.0.0.1:8788/
wire_api = "responses"
experimental_bearer_token = "sk-..."
```

`~/.codex/auth.json` 里放同一把 key：

```json
{"auth_mode": "apikey", "OPENAI_API_KEY": "sk-..."}
```

## 旧会话为什么必须保留 `provider = "custom"`

历史 rollout 里记录的是 `model_provider = "custom"`。Codex 的 `thread/list` 只列**当前
provider** 的线程，而且 resume 一个不存在的 provider 会直接报
`failed to load configuration: Model provider 'custom' not found`。所以：让 `custom`
指向 DeepSeek，老会话才能继续用；改回 `deepseek` 会让老会话在 App 里变成只能回看的
history-only。

## 模型名

官方 `https://api.deepseek.com/v1/models` 只列两个 id：`deepseek-flash`、`deepseek-v4-pro`。
`deepseek-v4-flash`、`deepseek-v4-flash-vision-exp` 这类旧名仍被接受，但会被计费成
`DeepSeek-V4.1-Flash`。

注意网关的模型名归一化：对 `provider: deepseek` 的分支，非 V 系列名字会被改写（旧版本会把
`deepseek-flash` 改写成 `deepseek-v4-flash`）。要保证名字原样透传，用 `provider: custom`
指向同一个官方 base_url 即可。

## 会话迁移提示

把老会话的 `modelLabel` / `modelOverride` 改到新模型时，建议保留原名字段（例如
`legacyModelLabel`）：一旦新模型不可用，还能知道它原本跑的是什么。
