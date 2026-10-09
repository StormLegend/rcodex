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

Release/deployment evidence is appended after publication and live verification.
