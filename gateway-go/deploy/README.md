# Independent gateway deployment

The Go gateway has its own LaunchAgent/systemd unit, data directory and release
layout. Keep the legacy Node gateway untouched. Use `upgrade-macos.sh` or
`upgrade-linux.sh` with a prebuilt binary. The script runs `-doctor`, copies the
binary into a version directory, atomically switches the `rcg` symlink, restarts
only the Go unit, checks `/healthz` for the requested version, and restores the
previous symlink on failure.

Pass the expected SHA256 as the fourth argument (or set
`RCG_EXPECTED_SHA256`) so the script rejects a tampered binary before creating
the release directory. The published GitHub `SHA256SUMS` file is the source for
that value.

Run `rcg-admin backup` before upgrades and keep the generated SQLite backup
outside the active data directory. Run `rcg-admin restore` only while the Go
unit is stopped; it validates the backup and preserves a pre-restore backup.
`rcg-admin rotate-token` rewrites a literal-token config atomically and prints a
new secret. Restart the Go unit after rotating it; clients must be updated
through the normal secret distribution channel.

`rcg-loadtest` exercises the read-only sessions API. A short run is not evidence
of a 24/72-hour soak; `soak-chain.sh` runs the 24-hour phase and then the
72-hour phase with a zero-failure gate. Both logs must reach their final
summaries before the soak is accepted.
