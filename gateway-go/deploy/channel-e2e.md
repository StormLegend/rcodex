# Real channel acceptance

The unit tests validate Telegram secret headers, Discord Ed25519 signatures,
Feishu challenge/encryption, allowlists and durable queue insertion. A real
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
