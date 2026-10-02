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
- Relay transport requires TLS (`wss`) outside loopback and uses yamux; relay
  connections use an authenticated `rcodex-relay/hello` control stream and
  bounded reconnect backoff.
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

### Relay deployment

Run `cmd/rcg-relay` on a public host with a TLS certificate. Start from
`relay.example.json`, generate independent connector and client secrets, and
install `deploy/rcodex-relay.service`. Configure the gateway with the same
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

The relay protocol is outbound from the gateway, so the Mac mini does not
need an inbound port or router port-forward. Keep the relay listener behind a
normal TLS certificate and rotate connector/access tokens independently.

The channel handlers enqueue work into durable sessions and the engine writes
provider responses to the outbox. Before production cutover, run external
acceptance tests with real bot credentials and finish relay peer
enrollment/rotation, stream forwarding policy, and the embedded console.
