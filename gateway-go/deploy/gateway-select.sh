#!/bin/sh
# Only changes the preferred independent endpoint; no service restarts or replay.
set -eu
if [ "$#" -lt 2 ]; then
  echo 'usage: gateway-select.sh TARGETS_JSON TARGET [REASON]' >&2
  exit 2
fi
exec "${RCG_OPS_BIN:-rcg-ops}" -config "$1" -command select -target "$2" -reason "${3:-operator selection}"
