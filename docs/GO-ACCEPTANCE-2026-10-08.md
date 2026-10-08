# Mac mini interactive acceptance — 2026-10-08

## Findings and changes

The previous v0.2.9 (18890) and v0.2.10-rc.1 (18891) services had healthy HTTP
listeners but pointed Codex at a missing plugin-cache executable. Claude was
also absent. Their HTTP soak results do not demonstrate model execution.
Their processes and configuration were preserved to keep their evidence intact.

Installed the official Codex 0.156.1 Darwin ARM64 package, verified against its
npm SHA512 integrity, under `~/.local/share/rcodex-runtime/codex/0.156.1` with
`~/.local/bin/codex` as a stable entry point. Its existing Mac ChatGPT login was
used; no Linux login credentials were copied. Claude Code 2.1.286 was installed
using the official installer with its manifest checksum verification. Claude
reports `loggedIn: false`; authenticated Claude acceptance remains open.

A separate `com.stormlegend.rcodex-go-preview` LaunchAgent serves loopback
18892. It owns separate configuration, tokens, SQLite data, logs and workspace.
It inherits the Mac's existing local HTTP proxy explicitly, since launchd does
not inherit interactive shell proxy settings. The released version for this
interactive track is v0.2.10-rc.3; rc.2 was an unpublished local validation build.

The embedded console now supports session creation, workspace/runtime/mode
selection, prompts, persistent history, streaming updates, cancellation,
approvals and structured question answers. Authenticated runtime status checks
executable installation without claiming account authorization. The API now
stores the resolved default workspace when a new session omits its path.

Desktop launchers open the authenticated local console or start the interactive
Claude login command. See [the usage guide](MACMINI-QUICKSTART.md).

## Executed acceptance

On the Mac preview instance, using real Codex and real ChatGPT authentication:

- Read `README.md` from the isolated workspace and returned the exact marker
  `MACMINI_WORKSPACE_READY_20261008`.
- Repeated the marker in a second turn without reading files, proving context
  continuation rather than a new isolated request.
- In `auto` mode, created `acceptance-output.txt`, read it back, and independently
  checked its contents on disk (`MACMINI_WRITE_OK_20261008`).
- Observed a real `sleep 90` command, cancelled the turn through the API, checked
  the `cancelled` state and confirmed no matching sleep process remained.
- Restarted only the preview gateway through the atomic upgrade path, then
  repeated the remembered marker without using tools, proving persistent resume.
- Ran Chromium against the Mac HTTP listener through an SSH tunnel: connected,
  created a session, sent two real prompts, verified conversation memory, reloaded
  the page, and checked persisted replies. No browser JavaScript errors.
- Inspected desktop and 390-pixel mobile layouts; corrected session-list row
  compression found during screenshot inspection.

Automated coverage:

- `go test ./...`, `go test -race ./...`, and `go vet ./...` passed locally.
- Browser tests cover authentication failures, token fragment removal, safe
  text rendering, history restoration, mobile width, approval/denial/question
  replies, cancellation, SSE cleanup, retry idempotency and path-prefix handling.
- Browser regression tests are included in GitHub CI alongside Linux/macOS Go
  tests. Browser fixtures are distinguished from the real Mac runs above.

## Existing service and soak evidence

At 2026-10-08 06:44 UTC:

| Instance | Version | Preserved process | HTTP soak |
| --- | --- | --- | --- |
| Mac 18890 | v0.2.9 | PID 40441, launchd runs 11 | 24h: 17,272 requests / 0 failures; 72h in progress, 20,390 requests / 0 failures |
| Mac 18891 | v0.2.10-rc.1 | PID 60233, launchd runs 1 | 24h: 17,272 requests / 0 failures; 72h in progress, 3,779 requests / 0 failures |

These counters are observations at that timestamp, not completed 72-hour results.
The new preview's real tasks do not transfer soak credit to its new binary.
The Linux user services `rcodex-gateway.service` and
`rcodex-gateway-dw2-cloudflared.service` remained active; old `/healthz` returned OK.

## Open production gates

Claude account authorization and real Claude tool/permission validation; dedicated
Telegram/Discord/Feishu credentials and permitted destinations; persistent provider
management and execution selection; attachment-to-runtime wiring; completion and
review of multi-day soak evidence. These remain open from the production goal.
Public Relay/Cloudflare deployment remains explicitly deferred. This is a usable
independent Mac verification instance, not a declaration of full production parity.
