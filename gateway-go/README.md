# rcodex-go

Go implementation of the gateway direction. It is deliberately independent of
the Node gateway and can run beside it on `127.0.0.1:8790`.

## Current production track

Implemented foundation:

- SQLite WAL persistence with crash recovery, active-turn uniqueness,
  idempotency keys, event cursors, approvals, outbox and backups.
- Codex app-server JSON-RPC process boundary and Claude Code
  `--output-format stream-json --input-format stream-json` process boundary.
- Workspace-root validation, bounded HTTP bodies, bearer auth, health and
  Prometheus-style metrics.
- Feishu challenge/encrypted callbacks, Telegram secret webhooks, Discord
  Ed25519 interaction verification, and explicit user/chat allowlists.
- Durable outbound delivery for Telegram, Discord interaction follow-ups, and
  Feishu tenant messages with retry/backoff and dead-letter state.
- Codex approval and question requests are persisted and can be resolved
  through the authenticated API; session events support cursor polling and
  Server-Sent Events.
- Relay transport uses a real WebSocket (`wss`) outside loopback, with yamux
  inside the WebSocket and an authenticated `rcodex-relay/hello` control
  stream. Gateway connections use bounded reconnect backoff.
- The standalone `rcg-relay` edge server accepts one authenticated connector
  per gateway and forwards authenticated client HTTP streams. `rcgctl` can use
  the same path with `-relay-url`, `-gateway`, and `-relay-token`.
- Graceful shutdown, restart recovery and a `doctor`/SQLite backup path.

The implementation is being developed beside the running Node gateway. It does
not alter the existing rcodex service or reuse its data directory.

## Run

```bash
export RCG_ACCESS_TOKEN="$(openssl rand -hex 32)"
cp gateway.example.json /tmp/rcodex-go.json
# edit ${RCG_ACCESS_TOKEN} and roots as needed
go run ./cmd/rcg -config /tmp/rcodex-go.json -doctor
go run ./cmd/rcg -config /tmp/rcodex-go.json
```

On macOS, use `deploy/com.stormlegend.rcodex-go.plist` with `launchctl
bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.stormlegend.rcodex-go.plist`.
Linux deployments can use `deploy/rcodex-go.service`.

### Browser console

Open `/console` on the gateway's local listener or HTTPS entry point. The
console supports creating Codex/Claude sessions, choosing a permitted workspace
and permission mode, sending tasks, viewing persistent history and streaming
updates, stopping tasks, and resolving approvals or runtime questions. Tokens
are stored in the current tab's session storage and cleared on disconnect.
`GET /api/runtime/status` requires bearer auth and reports executable availability,
workspace roots and permitted modes; it does not claim the runtime is logged in.

Sessions are grouped by source directory, with a separate collapsible Archive
folder and reversible archive/restore actions. Model and reasoning effort can
be selected at creation or changed between turns. Codex model choices follow
its local model cache. The composer shows actual per-turn token usage and the
latest reported context percentage; unavailable telemetry stays marked as such.
The SSE stream uses 10-second heartbeats and reconnects without replaying work.

`PATCH /api/sessions/{id}` accepts `model`, `effort`, `archived`, `mode` and
`title`. Active turns prevent settings changes; archived sessions must be
restored before enqueueing new work. History responses include the current
session and the latest listed turn's `usage` telemetry. `/api/models` preserves
its provider inventory and additionally returns a runtime model `catalog`.

On macOS, `python3 deploy/open-console-macos.py --config /path/to/gateway.json`
opens a local console with the token from its config. `--copy-token` copies it
to the clipboard without printing it. See the
[Mac mini verification guide](../docs/MACMINI-QUICKSTART.md) for the independently
managed preview instance and SSH access.

Browser regression checks are isolated from the gateway's Go build:

```sh
cd web-tests
npm ci --include=dev
npx playwright install chromium
npm test
```

### Relay deployment

Run `cmd/rcg-relay` on a public host with a TLS certificate. Start from
`relay.example.json`, generate independent connector and client secrets, and
set `gateway_token` to the gateway's API token. Install
`deploy/rcodex-relay.service`. Configure the gateway with the same
gateway ID and connector token:

```json
{
  "relay": {
    "url": "wss://relay.example.com:9443",
    "id": "macmini",
    "token": "the-connector-token-from-relay.json"
  }
}
```

The mobile/desktop client can use `rcgctl` over the yamux relay path, or any
normal HTTPS client through `http_listen`:

```bash
rcgctl -relay-url wss://relay.example.com:9443 \
  -gateway macmini -relay-token "$RCG_RELAY_TOKEN" \
  -command health
```

With the HTTP listener configured as `0.0.0.0:9444`, the equivalent standard
HTTP request is:

```bash
curl -H "Authorization: Bearer $RCG_RELAY_TOKEN" \
  https://relay.example.com:9444/v1/gateways/macmini/healthz
```

The HTTP listener is the intended integration point for iOS, Android and web
clients. It keeps the custom yamux protocol inside the relay-to-gateway link.
The relay validates the client access token and replaces it with
`gateway_token` before forwarding to the gateway, so the gateway API token is
never given to the phone app.

The relay protocol is outbound from the gateway, so the Mac mini does not
need an inbound port or router port-forward. Keep the relay listener behind a
normal TLS certificate and rotate connector/access tokens independently.

### Cloudflare Tunnel deployment

Cloudflare Tunnel is an independent HTTPS ingress option. It is useful when a
browser, webhook provider or native app should use a normal hostname. It can
run alongside Relay; both target the loopback gateway and keep their own
authentication layer. Use the templates under `deploy/cloudflared/` rather
than embedding a Cloudflare token in the gateway config.

The recommended origin is `http://127.0.0.1:18890` on the Mac mini. Keep the
rcodex Bearer token enabled even when Cloudflare Access is configured. Access
controls who reaches the hostname; the gateway token authorizes API operations.
Cloudflare's connector is a separate launchd/systemd service and maintains its
own outbound connections to the edge.

The channel handlers enqueue work into durable sessions and the engine writes
provider responses to the outbox. Before production cutover, run external
acceptance tests with real bot credentials and finish relay peer
enrollment/rotation, stream forwarding policy, and the embedded console.

### Independent operation and maintenance

The Go service is intentionally independent from the Node gateway. Use
`cmd/rcg-ops` with `deploy/targets.example.json` to probe both health endpoints
and explicitly select a healthy preferred endpoint. Selection never retries or
replays a turn on the other service because the two SQLite databases are not a
shared session store. `gateway-select.sh` is a small operator wrapper for the
same safe action.

The API includes session deletion and pagination, multipart attachments under
the gateway data directory, durable `once` / `every:duration` schedules, model
and provider inventory, and per-source rate limits. Schedules are persisted and
enqueued by the engine; a busy session is not replayed.

List endpoints return explicit cursors: `GET /api/sessions?limit=N` and
`GET /api/sessions/{id}/history?limit=N` return `next_before`; pass that value
back as `before` for the next older page. Session event history accepts
`after`, `before` and `limit`, returning `next_after` or `next_before` as
appropriate. A zero cursor means there is no additional page.

Run `cmd/rcg-admin backup` and `restore` for SQLite maintenance. Restore must be
performed with the Go service stopped; the command validates the input and
preserves a pre-restore backup. `rotate-token` atomically rewrites a literal
token config and requires a service restart. Use `deploy/upgrade-macos.sh` or
`upgrade-linux.sh` for versioned, health-gated upgrades and rollback. The
`rcg-loadtest` command and `deploy/soak.sh` exercise the read-only sessions API;
a short run is not evidence of a 24/72-hour soak.
