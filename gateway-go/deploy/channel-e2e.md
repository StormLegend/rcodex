# Real channel acceptance

The local integration test runs signed HTTP ingress, SQLite admission, a CLI
subprocess, durable outbox delivery, provider rejection, gateway/database restart,
and a successful retry for each provider. Duplicate ingress is checked before
and after restart. Separate tests verify the eight-attempt dead-letter limit,
provider error responses, Feishu verification tokens/signatures and independent
OpenSSL encryption vectors. Run:

```sh
cd gateway-go
go test -race ./internal/channels ./internal/web -count=1
```

These tests use loopback provider endpoints and deterministic model output. A real
acceptance run requires dedicated test destinations. Do not reuse the legacy
gateway's webhook or bot consumer: two consumers will produce duplicate or
missing deliveries.

For each provider, configure a separate test bot/app whose webhook points at the
Go gateway, send one message from an allowlisted test user and chat, wait for a
completed turn, and verify the reply arrives through the same provider. Record
the provider message/event ID, gateway turn ID, final state, delivery attempts,
and the dead-letter count. Repeat once after restarting the Go service to prove
the session binding and outbox survive a restart.

Required environment values are deliberately not committed:

- Telegram: bot token, webhook secret, allowlisted user ID and chat ID.
- Discord: application ID, public key, test channel/user and interaction token
  handling through a real interaction.
- Feishu: app ID/secret, verification token, encrypt key and test chat/user.

When those credentials are available, run the test against a staging URL or a
local tunnel and attach the redacted transcript to the release checklist. The
repository does not claim real external acceptance without this evidence.

`/healthz` includes `outbox_queued`, `outbox_sent`, and `outbox_dead`; the
authenticated `/metrics` endpoint exports the corresponding `rcg_` counters.
An accepted HTTP response is not sufficient for Telegram or Feishu: the response
must also contain the provider's success field. Outbound errors omit URLs and
response bodies, which can contain bot or interaction credentials. Redirects
are rejected. Discord messages disable automatic mentions.

Delivery remains at-least-once: a crash after provider acceptance but before the
SQLite acknowledgement can repeat a reply. Discord interaction tokens expire;
delayed jobs may end in the dead-letter queue. Long-response splitting,
provider-specific retry timing and operator dead-letter replay still need work
before broad unattended bot use.
