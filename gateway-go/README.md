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
- Feishu challenge callback, Telegram secret webhooks, Discord Ed25519
  interaction verification; all channels require explicit user/chat allowlists.
- Relay transport requires TLS (`wss`) outside loopback and uses a multiplexed
  authenticated transport boundary.
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

The channel handlers are webhook ingress and enqueue work into durable sessions.
Actual bot API delivery, Relay enrollment/identity rotation, interactive
approval transport, and the embedded production console remain in the next
implementation slice; they must pass external bot and domain acceptance tests
before a production cutover.
