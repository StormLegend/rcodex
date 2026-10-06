# Go gateway acceptance — 2026-10-06

Result: substantial regressions found and repaired; **not a final production
sign-off**. Existing Node services and the Mac mini v0.2.9 gateway/soak were kept
running. All destructive/failure scenarios used separate temporary directories,
databases, listener ports and deterministic test credentials.

## Findings repaired

- Claude could report an error with exit code zero, or exit without a result,
  while the gateway marked the turn completed. Result envelopes are checked.
  Handler/scan errors now close pipes and wait for the child. Cancellation and
  timeout retain the correct terminal state.
- Codex resume after gateway restart did not start/initialize app-server, and
  starting a second session initialized the existing process again. Initialization
  now runs once per process, with its `initialized` notification. Resume starts a
  fresh process when necessary. Blocking protocol reads can be cancelled and
  child processes are reaped. A waiting operation can cancel without taking over
  the active operation's lock.
- Codex `readonly` selected workspace-write. It now uses `read-only` /
  `readOnly` with approval policy `never`. Only a matching turn completion can
  finish a turn; idle notifications and failed completions are not successes.
  Interrupt requests include both thread and turn IDs.
- A failed native-session-ID write could be ignored before executing a turn.
  Execution now stops when identity persistence fails.
- SQL NULL responses made pending approvals unreadable. Pending and resolved
  records now scan correctly. Automatic approval decisions are recorded as
  resolved, with manual-denial and automatic-acceptance integration coverage.
- Feishu used the wrong AES IV and did not authenticate normal events. It now
  reads the prefixed IV, checks padding, requires configured encryption, verifies
  the request signature/freshness and header token, and extracts text from the
  message-content JSON. Unsupported message types do not enqueue model work.
- Telegram/Feishu HTTP 200 business errors were acknowledged as sent. Provider
  success envelopes are now required, so failures remain queued. Redirects are
  rejected and error logs omit credential-bearing URLs/bodies. Discord supports
  direct-message identity and disables automatic mentions.
- `rcg -doctor` and `-backup` ran crash recovery against a live database. Only
  the serving startup path performs recovery now. Subprocess tests verify that
  maintenance leaves running turns and pending approvals intact.

## Evidence and scope

| Acceptance | Result / limits |
| --- | --- |
| Real Codex over HTTP | Passed on Linux with the installed, authenticated CLI. First turn remembered `RCG-ACCEPTANCE-7B42`; after stopping/restarting the isolated Go gateway, a second turn recalled it without the prompt supplying it again. Native ID remained identical. Completed at 2026-10-06 13:56:22 UTC. No tools were requested. |
| Claude lifecycle | Real subprocess + SQLite close/reopen passed for first turn, `--resume`, cancellation, timeout, handler failure, unsuccessful result, and missing result. Model output is a fixture; this does not establish authenticated Claude/tool behavior. |
| Telegram / Discord / Feishu | Signed HTTP ingress → SQLite → engine → CLI fixture → outbox → failed provider delivery → gateway/database restart → successful retry passed for all three. Duplicates before/after restart produce one turn. Providers are local HTTP fixtures. |
| Delivery exhaustion | Real outbox worker reaches `dead` after eight failures and no longer selects the item. Test advances stored due times to avoid waiting through backoff; it is not a multi-minute timing soak. Dead-letter counts are exposed in health/metrics. |
| Approval handling / maintenance | Pending and resolved reads, runtime approval responses, native-ID persistence failure, doctor and backup against active data pass. |
| Linux checks | `go test -race ./... -count=1 -timeout=120s` and `go vet ./...` pass. New tests also fail against the original `54fe667` implementation, establishing regression sensitivity. |
| Mac mini | Cross-compiled native arm64 test executables exercise runtime, engine, channels, store, web and maintenance. An isolated Go HTTP process verifies completed/cancelled/timed-out/failed turns, restart identity and online maintenance. Model/provider output is simulated. |
| Build matrix | Six commands compile for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and windows/amd64 (30 executables). Compilation does not establish native Windows behavior or Windows descendant-process cleanup. |

The Mac test fixture also needed to canonicalize its temporary workspace root:
macOS `/var` resolves through `/private/var`, as the actual config loader already
handles. The workspace boundary was not weakened to make the test pass.

CI now runs tests, the race detector and vet on Linux and macOS. Tagged releases
run validation before publishing; tags containing a suffix publish as prereleases.

## Published candidate and independent Mac deployment

- Source/tag commit: `7f5802c3995478ef716694d48cca317c048236af`.
  [Linux/macOS CI](https://github.com/StormLegend/rcodex/actions/runs/37475759324)
  and the [release workflow](https://github.com/StormLegend/rcodex/actions/runs/37475816011)
  completed successfully.
- [v0.2.10-rc.1](https://github.com/StormLegend/rcodex/releases/tag/v0.2.10-rc.1)
  is a prerelease with five platform archives (six programs each) and `SHA256SUMS`.
  The downloaded darwin/arm64 archive was checked against both the manifest and
  GitHub asset digest:
  `8329b1471942a5c3cb85862662831b8f5d7e1039975f2459138f4fe4776ad59b`.
- Mac mini's candidate is launchd-managed as
  `com.stormlegend.rcodex-go-candidate`, listening only on `127.0.0.1:18891`.
  It has separate tokens, database, workspace and logs. Bot channels and Relay
  are disabled in this acceptance instance. Config is owner-readable only at
  `~/Library/Application Support/rcodex-go-candidate/gateway.json`.
- The published executable passed another isolated HTTP lifecycle/maintenance
  smoke test on Mac. At 2026-10-06 14:12:49 UTC the candidate was healthy (PID
  60233), and its authenticated sessions API returned 200. The pre-existing
  v0.2.9 instance on port 18890 retained PID 40441 and also returned healthy/200.
  The old Linux Node unit and its tunnel remained active.
- Candidate 24h → 72h read-only soak started at **2026-10-06 14:10:42 UTC** under
  `com.stormlegend.rcodex-go-candidate-soak` (supervisor PID 60235, chain 60237,
  load generator 60239). State is in
  `~/Applications/rcodex-go-candidate/soak-chain.json`; logs are in
  `~/Library/Logs/rcodex-go-candidate/soak-v0.2.10-rc.1/`.
  The supervisor refuses to overwrite an existing run on reload/reboot. Check
  actual processes and final summaries as well as metadata before accepting it.
- The v0.2.9 baseline soak was not restarted. Its last inspected report was
  **16,312 requests, 0 failed**; this was a progress line, not a completed phase.
  Neither run establishes sustained model execution or real bot delivery.

## Protocol references

The Codex contract was checked against JSON schemas generated by the locally
installed `codex app-server generate-json-schema`. The real restart exercise
above independently verifies the basic handshake, turn and resume contract.

Feishu framing and signatures were checked against its
[official Go SDK at commit 99927aa](https://github.com/larksuite/oapi-sdk-go/blob/99927aa13e271ea9fe03591204aad7bc6a2d869c/event/event.go)
and [encryption implementation](https://github.com/larksuite/oapi-sdk-go/blob/99927aa13e271ea9fe03591204aad7bc6a2d869c/core/utils.go).
The regression vector is independently generated using OpenSSL AES-256-CBC,
SHA256 of the encryption key, a known IV prefix and PKCS#7 padding. Telegram's
[official response contract](https://core.telegram.org/bots/api#making-requests)
requires checking `ok` in addition to the HTTP status.

## Still open

- Real dedicated Telegram/Discord/Feishu accounts, allowed test destinations,
  callback registration and observed provider replies. No legacy bot consumers
  or webhook registrations were changed.
- Real Claude authentication and tool-permission acceptance. Local Claude reports
  not logged in; no Claude executable was found in the Mac mini's configured or
  common installation locations during this run.
- Persistent provider management/execution selection, attachment-to-runtime
  consumption, and remaining Go/Node parity. Provider/model endpoints currently
  describe configuration; this is not a completed management API.
- Large provider responses, provider-specific retry timing and dead-letter
  operator replay. Outbound delivery is at-least-once, not exactly-once.
- Completed 24/72-hour evidence for the exact release candidate. The existing
  v0.2.9 low-rate read-only soak remains in progress and cannot qualify new code
  or external model/bot behavior. It must not be described as completed merely
  because an elapsed-time estimate is close to 24 hours.
- Public Relay/Cloudflare deployment remains explicitly deferred (item 1).
