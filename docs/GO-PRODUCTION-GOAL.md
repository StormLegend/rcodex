# Go gateway production goal

Started 2026-10-05. Owner: Codex. User explicitly requests two independent
services. Keep the existing Node service running. Public Relay / Cloudflare
deployment (item 1) is deferred. Do not claim this is a transparent HA cluster:
each gateway owns its sessions and data, and in-flight operations cannot move.

## Ordered acceptance checklist

2. [x] Independent endpoint health probes and controlled selection with saved
   state, unhealthy-target rejection, failure/recovery thresholds, tests and a
   rollback procedure. Never replay mutating requests on another gateway.
3. [x] Claude session identity persisted across turns/restarts, explicit resume,
   cancellation/timeout that reaps processes, stream/error handling, permission
   semantics and runtime/engine integration tests.
4. [x] Session deletion, attachments, paginated history, persistent schedules,
   runtime model/provider configuration APIs. Validate authorization, path
   confinement, restart behavior and compatibility with existing SQLite data.
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
  release tags through `v0.2.7` are present.
- GitHub Release `v0.2.7` exposes `darwin_amd64`, `darwin_arm64`,
  `linux_amd64`, `linux_arm64`, `windows_amd64` archives and `SHA256SUMS`;
  the release endpoints returned HTTP 200 and the published manifest was
  downloaded for verification.
- GitHub workflow `.github/workflows/gateway-go-ci.yml` now runs unit tests,
  the race detector and `go vet` for gateway changes on pushes and pull
  requests; the same checks pass locally after the latest test-only change.
- Mac mini: independent launchd service `com.stormlegend.rcodex-go`, loopback
  `127.0.0.1:18890`, separate config and database; v0.2.7 deployed.
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
- A chained low-rate 24-hour then 72-hour read-only soak is running on Mac mini
  as PID 39198 using an environment token (not a command-line argument). The
  chain has a zero-failure gate and keeps separate phase logs; its final result
  is intentionally pending until both phases exit. The initial test token was
  rotated immediately after a process-list visibility check. Mac mini also has
  a non-secret metadata record at `Applications/rcodex-go/soak-chain.json`.
- Latest observed 24-hour phase report: `requests=60 failed=0`; the 72-hour
  phase will be created only after the first phase exits successfully.
- A fresh local cross-build matrix produced all six binaries for each of
  `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64` and
  `windows/amd64`; the same matrix is published by the tag release workflow.
- Health transition alert delivery has a regression test covering the POST
  JSON webhook path.
