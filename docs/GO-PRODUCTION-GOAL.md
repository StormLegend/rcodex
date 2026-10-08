# Go gateway production goal

Started 2026-10-05. Owner: Codex. User explicitly requests two independent
services. Keep the existing Node service running. Public Relay deployment remains deferred. On 2026-10-08 the user explicitly
authorized Cloudflare Tunnel for myrcodex.19920621.xyz; that named ingress is now deployed. Do not claim this is a transparent HA cluster:
each gateway owns its sessions and data, and in-flight operations cannot move.

## Ordered acceptance checklist

2. [x] Independent endpoint health probes and controlled selection with saved
   state, unhealthy-target rejection, failure/recovery thresholds, tests and a
   rollback procedure. Never replay mutating requests on another gateway.
3. [partial] Claude session identity persisted across turns/restarts, explicit resume,
   cancellation/timeout that reaps processes, stream/error handling, permission
   semantics and runtime/engine integration tests. Local protocol/subprocess
   coverage passes; authenticated Claude and real tool permissions remain open.
4. [partial] Session deletion, attachments, paginated history, persistent schedules,
   runtime model/provider configuration APIs. Validate authorization, path
   confinement, restart behavior and compatibility with existing SQLite data.
   Current provider/model endpoints expose configured inventory; they do not
   implement persistent provider management or provider selection for execution.
5. [partial] Telegram, Discord, Feishu: local signed ingress -> queue -> runtime ->
   durable outbound tests, then real dedicated bot acceptance. Real credentials
   and permitted test destinations requested; never take over an old bot's
   webhook/consumer. External acceptance must be reported separately.
6. [partial] Key rotation, limits, health alerts, isolated backup/restore drill, load /
   failure tests and actual elapsed 24-hour and 72-hour soak evidence. A short
   simulation cannot count as a completed multi-day soak.
7. [x] Versioned release directories, checksum validation, atomic switch,
   health-gated rollback; ship binaries and deploy only the independent Go
   service on Mac mini. Preserve the previous release and data backups.

## Baseline evidence

- Repository: `StormLegend/rcodex`, clean `main` at the latest pushed commit;
  release tags through `v0.2.9` are present.
- GitHub Release `v0.2.9` exposes `darwin_amd64`, `darwin_arm64`,
  `linux_amd64`, `linux_arm64`, `windows_amd64` archives and `SHA256SUMS`;
  the release endpoints returned HTTP 200 and the published manifest was
  downloaded for verification.
- GitHub workflow `.github/workflows/gateway-go-ci.yml` now runs unit tests,
  the race detector and `go vet` for gateway changes on pushes and pull
  requests; the same checks pass locally after the latest test-only change.
- Mac mini: independent launchd service `com.stormlegend.rcodex-go`, loopback
  `127.0.0.1:18890`, separate config and database; v0.2.9 deployed through the
  health-gated atomic upgrade script. v0.2.8 remains available for rollback.
- Old Linux Node service and its existing tunnel remain active.
- Last baseline verification: Go tests, race detector, vet, Mac health and
  authenticated sessions API passed. These do not establish full parity.

## Work log

- Goal started; inspected current runtime, engine, store, API and deployment.
- Found cancellation endpoint only cancels queued turns, Claude session IDs
  are not persisted, and maintenance commands recover active turns. These
  require regression coverage during the corresponding steps.
- Added and tested `rcg-ops`, `rcg-admin`, `rcg-loadtest`, API lifecycle,
  persistent schedules, Claude resume/cancellation and versioned deployment.
- Pushed commit `f773e9b` and tag `v0.2.0`; Mac mini runs `v0.2.0` in
  `releases/v0.2.0/rcg`, while the legacy Node service remains active.
- Local signed channel and provider outbound fixtures pass. Real Claude smoke reached the installed
  CLI but reported `Not logged in`; real Telegram/Discord/Feishu credentials
  were not present, so external acceptance remains open.
- Short concurrent load test and backup/restore/rotation drills pass. A real
  24-hour and 72-hour elapsed soak still needs to run and be recorded.
- The historical v0.2.7 chained soak used an environment token (not a
  command-line argument), a zero-failure gate and separate phase logs. It was
  stopped before the v0.2.8 cancellation fix and its token was rotated after a
  process-list visibility check.
- A fresh local cross-build matrix produced all six binaries for each of
  `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64` and
  `windows/amd64`; the same matrix is published by the tag release workflow.
- Health transition alert delivery has a regression test covering the POST
  JSON webhook path.
- v0.2.8 fixes Claude cancellation classification and process-group cleanup;
  the regression test confirms cancellation returns `context.Canceled` and
  reaps a child process that has spawned a descendant.
- v0.2.9 adds `next_before` / `next_after` cursors to session, history and
  event APIs; store and HTTP tests exercise fetching the following page.
- A fresh isolated `rcg-admin` drill completed backup, restore, quick check,
  pre-restore preservation and literal-token rotation without touching the
  Mac mini data directory.
- Before upgrading, the Go SQLite database was backed up to the Mac mini
  `Applications/rcodex-go/backups/pre-v0.2.9-*.db` path and the new release
  passed `-doctor` plus the live health version gate.
- The macOS/Linux upgrade scripts now reject a supplied SHA256 mismatch before
  creating a release directory; shell syntax and a negative checksum gate were
  verified locally.
- An isolated upgrade drill verified both the SHA256-accepted atomic switch and
  health-gated rollback to the previous release symlink.
- A live `rcg-ops` run probed the legacy Node health endpoint on `127.0.0.1:8787`
  and the Mac mini Go endpoint through a temporary SSH loopback forward; both
  were healthy and explicit selection persisted `go` without replaying a turn.
- The v0.2.8 soak was intentionally stopped before the pagination binary
  change. A new environment-token chained soak is now running as v0.2.9 PID
  40470 with separate logs under
  `Library/Logs/rcodex-go-soak-chain-v0.2.9`.

## 2026-10-06 acceptance continuation

The earlier completed markers for items 3 and 4 overstated the evidence and have
been corrected. See [the acceptance report](GO-ACCEPTANCE-2026-10-06.md) for the
regressions, tests, real Codex restart result, and remaining production gates.
The v0.2.9 soak remains a baseline for that exact binary; its elapsed time cannot
be transferred to the new candidate. Public ingress deployment remains deferred.

Release `v0.2.10-rc.1` and all five platform archives are published. Linux/macOS
CI and release validation passed. Mac mini now runs an additional isolated
launchd candidate on `127.0.0.1:18891`, with its own config/data/workspace and a
separate 24h→72h soak started at 2026-10-06 14:38:01 UTC after a passing short
probe. The first candidate soak's wrong endpoint returned 404; that failed round
was archived, and a fail-fast preflight was added. The existing Go service
on 18890, its v0.2.9 soak, and the old Linux Node service remain unchanged.
See the report for labels, log locations and verification timestamps.

## 2026-10-08 Mac interactive verification

Added an independent preview on loopback 18892, keeping the 18890/18891 processes
and their soak runs intact. Found that their configured Codex executable was
absent: prior HTTP health/soak evidence had not validated model execution. Installed
managed Codex and Claude runtimes, used the Mac's existing Codex login, and added
a browser console that can create sessions, send/cancel tasks, and resolve
approvals/questions. Real Mac Codex read, write, context continuation, cancellation,
gateway-restart resume, and browser conversation/reload checks passed. Claude
is installed but not authenticated. Both older instances completed their 24-hour
HTTP phases with 17,272 requests and zero failures; their 72-hour phases are still
running. See [the acceptance record](GO-ACCEPTANCE-2026-10-08.md) and
[Mac usage guide](MACMINI-QUICKSTART.md). Remaining production gates stay open.

Published `v0.2.10-rc.3` from `6ff0d69`; Linux, macOS, browser and release checks
passed. The Mac preview now serves the checksum-verified GitHub Darwin ARM64
artifact from `releases/v0.2.10-rc.3-github`, with a pre-upgrade SQLite backup.
A real Codex resume turn and browser checks passed on that published binary.

## 2026-10-08 console and Cloudflare follow-up

The user explicitly authorized `myrcodex.19920621.xyz`. An independent named
Cloudflare Tunnel now exposes only the Mac preview's loopback listener. The
macOS-inspired console adds modal setup, a compact sidebar, light/dark appearance,
conversation search and safe Markdown/code copying. A real authenticated Codex
turn and SSE/history checks passed through the public HTTPS hostname. See
[the deployment and acceptance record](GO-UI-TUNNEL-2026-10-08.md).

`v0.2.11-rc.1` is published from `2987334`; all CI and release checks passed.
Mac preview now uses the verified GitHub package. Public HTTPS root/auth/history,
SSE replay and responsive appearance checks passed on the published artifact.
The existing Mac/Linux services remain running independently.
