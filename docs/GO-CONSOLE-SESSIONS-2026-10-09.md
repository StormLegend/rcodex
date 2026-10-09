# Console sessions and stream reliability · 2026-10-09

This update targets the independent Mac preview gateway on `127.0.0.1:18892`.
The Node gateway, FRP, existing Cloudflare connectors, and other Go instances
are outside its deployment scope.

## Delivered behavior

- Session folders follow the source working directory, with full paths in the
  folder tooltip and searchable by path. Archived sessions live in a separate,
  initially collapsed Archive folder. Archive/restore preserves all history;
  archived sessions reject new turns until restored. Folder expansion and the
  last active selection survive reloads.
- A separate SQLite `session_preferences` table stores archive state, effort
  and the original directory. The original eight-column sessions table remains
  compatible with the preceding binary. On startup, legacy `import-<native ID>`
  rows are matched read-only against Codex `state_5.sqlite`; previously saved
  gateway preferences always win. Execution workspaces stay within configured
  roots, even when an imported source directory belongs to another machine.
- Model and reasoning selectors appear both at creation and above the composer.
  Codex choices come from its installed model cache, excluding hidden models.
  Claude CLI aliases and configured provider model IDs are also available.
  Settings are persisted, checked against supported efforts where known, and
  locked during active turns. Codex receives `turn/start.model` and `effort`;
  Claude receives `--model` and `--effort`.
- Per-turn input/output usage is derived from actual runtime events. Codex
  cumulative thread counters are converted to turn deltas; duplicate snapshots
  and counter resets are handled. Context percentage uses the latest request's
  tokens divided by the runtime-reported context window. Cached input is part
  of input, not counted twice. Missing telemetry is explicitly unavailable.
  Claude result totals and assistant usage are parsed; mixed-model windows do
  not produce an invented percentage.
- Each event stream flushes immediately, sends a heartbeat every 10 seconds,
  and uses a bounded deadline per write instead of the server's absolute
  30-second response deadline. Cursor replay and reconnect backoff preserve the
  session without resubmitting a turn. Browser refreshes are throttled.

## Failure diagnosis

The previous HTTP server applied `WriteTimeout: 30s` to SSE handlers, with no
initial flush or heartbeat. This can disconnect the event stream even on
loopback. Mac launchd showed the preview process had not restarted during the
reported interruptions. Local loopback removes the network hop, but not this
application timeout. Model requests still use the Mac's external proxy path;
this fix does not claim to remove every possible upstream failure.

## Verification

- `go test -race ./...`, `go vet ./...` and browser acceptance tests pass.
- Regression coverage includes migration/rollback schema compatibility,
  archive/restore and busy-session guards, hidden-model filtering, unsupported
  effort rejection, Codex/Claude settings propagation, usage/reset accounting,
  505-session pagination and grouping, mobile layout, and reconnect without
  duplicate submission.
- A real HTTP stream remains writable past a deliberately short server write
  deadline; Mac isolation on `18893` stayed connected for 70 seconds, receiving
  seven heartbeats with zero unexpected EOFs.
- The isolated copy restored 472 imported sessions across 96 original
  directories, including 151 archives. Existing model/effort metadata is
  recovered without changing allowed execution paths.
- Real Codex turns switched from `gpt-6-luna/low` to `gpt-6-astra/high`, retained
  the first turn's exact marker, and exposed actual token/context telemetry.
  Codex's native database independently confirmed `gpt-6-astra` and `high`.
  Archive/restore, reload and desktop/mobile browser checks passed.
- Claude argument and usage parsing have automated coverage. Authenticated
  Claude execution remains an existing acceptance gap; no successful live
  Claude call is claimed here. A 70-second stream check is not a multi-day soak.

## Published release and Mac installation

- Source commit: `e6e6e002e0629c1a813037dda101478e06a6997d`.
- [Linux/macOS/browser CI](https://github.com/StormLegend/rcodex/actions/runs/37881015384)
  and [release validation/build](https://github.com/StormLegend/rcodex/actions/runs/37881045847)
  both passed.
- [v0.2.12-rc.1](https://github.com/StormLegend/rcodex/releases/tag/v0.2.12-rc.1)
  publishes all six programs for Linux amd64/arm64, macOS amd64/arm64 and Windows
  amd64, plus `SHA256SUMS`.
- The Mac arm64 archive SHA256 is
  `2414818c25611c2547a05f277abe0d9b81299ef2f6523898dd738dbe47cfc4cf`;
  its installed `rcg` binary SHA256 is
  `d05a3f81b5a1f6b8eaad7e9eef70b2a31545ea603bdf3bc589b062eb3d5e5540`.
  Both the published asset digest and the downloaded manifest were checked.
- Installed under `~/Applications/rcodex-go-preview/releases/v0.2.12-rc.1-github`.
  The atomic symlink upgrade retained `v0.2.11-rc.1-github` for rollback. A backup
  taken with the preceding binary before schema initialization is at
  `backups/pre-v0.2.12-rc.1-github.db`.
- Published-binary acceptance on `18892` passed a real Codex turn, usage display,
  archive/restore, reload, mobile layout and the public browser console. The
  temporary gateway test session was deleted after verification; the original
  478 sessions remain, with 151 archives and 96 imported directories.
- Loopback SSE on the published binary stayed open for 70 seconds and seven
  heartbeats with zero unexpected disconnects. The authenticated public browser
  stream through `myrcodex.19920621.xyz` subsequently passed 70.7 seconds and
  seven heartbeats, also with no unexpected disconnects. Database `quick_check`
  is `ok`.
- Preview launchd PID after upgrade: `4761`. Other Go ports `18890` and `18891`
  retained PIDs `40441` and `60233`. The Mac Cloudflare connector retained PID
  `87917`. Linux legacy Node/FRP/Cloudflare PIDs remained
  `2935418` / `4182386` / `2503614` respectively.
- The disposable `18893` acceptance process was stopped after testing. Its
  copied database never ran schedules or outbound deliveries.

During verification, Cloudflare rejected Python urllib's default user agent
with edge error `1010` (HTTP 403). Public acceptance uses a real browser; no
zone security policy was changed for this console update. A first public browser
navigation returned Chromium `ERR_NETWORK_CHANGED` on the test client; retrying
navigation succeeded before the 70.7-second stream observation. This is separate
from the fixed server write deadline and is not counted as a clean first attempt.

## Goal timeout follow-up · 2026-10-09

The original `turn_seconds: 300` in the Go preview was a generic defensive
ceiling, separate from the SSE timeout. It was too restrictive for Goal-style
work. `v0.2.12-rc.2` changes `turn_seconds: 0` to mean no artificial execution
deadline and keeps positive values as an explicit operator-selected hard cap.
The engine still ends work on explicit cancel, runtime failure, parent shutdown,
or a configured positive deadline. Negative values are rejected.

This matches the legacy Node gateway's behavior: its HTTP server leaves the
response timeout unset, sends an SSE ping every 25 seconds, and does not wrap a
whole turn in a wall-clock deadline. Its 120-second app-server request timer
only guards JSON-RPC control requests such as `thread/start` and `turn/start`;
the turn continues through notifications after a turn ID is returned. Human
approval waits use a separate 600-second timer. The old gateway therefore does
not treat a long Goal as an unanswered HTTP request.

The published Go service now uses `turn_seconds: 0` on the independent Mac
preview. The legacy Node service and all other services were left unchanged.

## Long-running Goal release follow-up

- [v0.2.12-rc.2](https://github.com/StormLegend/rcodex/releases/tag/v0.2.12-rc.2)
  contains the no-artificial-deadline engine behavior. Release CI passed for
  all platform artifacts; the Mac arm64 archive SHA256 is
  `5709b7b4ed6b5a6476ada0230ed05d8931c17551e89d03ae04617d489afbe73a`.
- Mac mini now points `rcodex-go-preview/rcg` to
  `releases/v0.2.12-rc.2-github/rcg`, with `turn_seconds: 0`; health reports
  `v0.2.12-rc.2`, SQLite `quick_check` is `ok`, there are 478 sessions and no
  queued/running turns. The pre-upgrade online SQLite backup is
  `backups/pre-v0.2.12-rc.2-github.db`.
- The previous independent release remains available at
  `releases/v0.2.12-rc.1-github/rcg` for rollback. Old services were not
  restarted or reconfigured.
