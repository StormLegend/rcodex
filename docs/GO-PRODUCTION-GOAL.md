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
  release tags through `v0.2.3` are present.
- Mac mini: independent launchd service `com.stormlegend.rcodex-go`, loopback
  `127.0.0.1:18890`, separate config and database; v0.2.3 deployed.
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
- A low-rate 24-hour read-only soak is running on Mac mini as PID 38498 using
  an environment token (not a command-line argument); its final result is
  intentionally pending until the process exits. The initial test token was
  rotated immediately after a process-list visibility check.
