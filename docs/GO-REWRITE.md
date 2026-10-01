# Go gateway implementation contract

The Go gateway is an independent protocol and implementation. The Node gateway
remains available during migration; neither its systemd service nor data is reused.

## Required delivery

- Single Go executable: gateway, relay, doctor, database backup commands.
- SQLite WAL: sessions, durable turn queue, cursor-indexed events, approvals,
  channel deduplication, outbox retries, scheduled jobs. Interrupted execution is
  surfaced after restart; potentially side-effecting turns are never auto-replayed.
- Codex app-server and Claude Code CLI adapters with resume, streamed events,
  timeouts, process cleanup, explicit permission modes and approval responses.
- Feishu encrypted callbacks, Telegram secret webhooks, Discord signed slash
  commands; explicit operator/chat allowlists and durable notification delivery.
- Authenticated outbound multiplexed Relay, gateway isolation, stream limits,
  reconnect/backoff, SSE/WebSocket proxying. TLS required off loopback.
- Authenticated API, embedded console, session/files/attachments/history/usage,
  schedules, graceful shutdown, health/metrics, non-root deployment.
- Unit, integration, race, restart, load, protocol and real-runtime smoke tests;
  credentials-dependent channel tests are reported separately.

## Boundaries

This is a single-operator trusted gateway, not a hostile multi-tenant hosting
platform. Workspace checks constrain the HTTP file APIs and runtime cwd; full
runtime access is an explicit privilege, not a container sandbox. Production
rollout requires real test bots, TLS/domain configuration, backup drills and an
operator acceptance window. No existing service is replaced during development.

Implementation tree: `gateway-go/`. The first Go foundation is intentionally not wired to the existing service.
