#!/bin/sh
# Run a real elapsed soak; do not confuse a short CI run with 24/72-hour evidence.
set -eu
if [ "$#" -lt 1 ]; then echo "usage: $0 URL [TOKEN] [DURATION] [CONCURRENCY]" >&2; exit 2; fi
URL=$1
TOKEN=${2:-}
DURATION=${3:-24h}
CONCURRENCY=${4:-4}
exec "${RCG_LOADTEST_BIN:-rcg-loadtest}" -url "$URL" -token "$TOKEN" -duration "$DURATION" -concurrency "$CONCURRENCY"
