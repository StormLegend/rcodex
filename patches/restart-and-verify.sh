#!/usr/bin/env bash
# Restart the gateway, wait until it answers, verify the patch set is still
# applied, and optionally probe a public URL.
#
#   RCODEX_PUBLIC_URL=https://gateway.example.com patches/restart-and-verify.sh \
#     [gateway-target] [node-binary]
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
gateway_target="${1:-$(npm root -g)/@rcodex-lab/gateway}"
node_bin="${2:-${RCODEX_NODE_COMMAND:-}}"
local_health_url="${RCODEX_LOCAL_URL:-http://127.0.0.1:8787}"
public_url="${RCODEX_PUBLIC_URL:-}"

if [[ -z "$node_bin" ]]; then
  node_bin="$(command -v node || true)"
fi

if [[ -z "$node_bin" || ! -x "$node_bin" ]]; then
  echo "A working Node.js executable must be passed as the second argument" >&2
  exit 127
fi

systemctl --user daemon-reload
systemctl --user restart rcodex-gateway.service

healthy=false
for _attempt in $(seq 1 30); do
  if curl -fsS --max-time 2 "$local_health_url/console" >/dev/null; then
    healthy=true
    break
  fi
  sleep 1
done

if [[ "$healthy" != "true" ]]; then
  echo "Gateway did not become healthy within 30 seconds" >&2
  systemctl --user status rcodex-gateway.service --no-pager >&2 || true
  exit 1
fi

"$node_bin" "$repo_root/patches/patch-gateway.mjs" \
  --target "$gateway_target" \
  --verify-only \
  --strict

echo "Local console health: 200 ($local_health_url)"

if [[ -n "$public_url" ]]; then
  public_healthy=false
  for _attempt in $(seq 1 30); do
    if curl -fsS --max-time 5 "$public_url/console" >/dev/null; then
      public_healthy=true
      break
    fi
    sleep 1
  done
  if [[ "$public_healthy" != "true" ]]; then
    echo "Public console ($public_url) did not become healthy within 30 seconds" >&2
    exit 1
  fi
  echo "Public console health: 200 ($public_url)"
else
  echo "Public console check skipped (set RCODEX_PUBLIC_URL to enable)"
fi
