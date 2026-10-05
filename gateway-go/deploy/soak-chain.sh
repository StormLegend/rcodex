#!/bin/sh
# Run the required 24-hour soak, then the 72-hour soak, with a zero-failure
# gate. Credentials are read from RCG_ACCESS_TOKEN and never passed as args.
set -eu
if [ "$#" -lt 1 ]; then echo "usage: $0 URL [LOG_DIR]" >&2; exit 2; fi
URL=$1
LOG_DIR=${2:-"$PWD/rcg-soak-logs"}
BIN=${RCG_LOADTEST_BIN:-rcg-loadtest}
mkdir -p "$LOG_DIR"
export RCG_ACCESS_TOKEN=${RCG_ACCESS_TOKEN:-}
"$BIN" -url "$URL" -duration 24h -concurrency 1 -pause 5s -report-interval 5m -max-failed 0 >"$LOG_DIR/24h.log" 2>&1
"$BIN" -url "$URL" -duration 72h -concurrency 1 -pause 5s -report-interval 5m -max-failed 0 >"$LOG_DIR/72h.log" 2>&1
