#!/usr/bin/env bash
# Install (or upgrade) the maintained rCodex Gateway overlay.
#
#   1. installs @rcodex-lab/gateway from npm (we never redistribute it ourselves)
#   2. applies the patch set and verifies it strictly
#   3. installs the systemd user hook that re-applies patches after npm upgrades
set -euo pipefail

GATEWAY_VERSION="${GATEWAY_VERSION:-1.4.37}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if ! command -v node >/dev/null 2>&1; then
  echo "error: Node.js >= 22 is required" >&2
  exit 1
fi
NODE_MAJOR="$(node -p 'process.versions.node.split(".")[0]')"
if [ "$NODE_MAJOR" -lt 22 ]; then
  echo "error: Node.js >= 22 is required (found $(node -v))" >&2
  exit 1
fi

if [ ! -d "$SCRIPT_DIR/patches" ]; then
  echo "error: patches/ not found next to install.sh (run it from a git clone)" >&2
  exit 1
fi

echo "==> installing @rcodex-lab/gateway@${GATEWAY_VERSION} from the npm registry"
npm install -g "@rcodex-lab/gateway@${GATEWAY_VERSION}"

GATEWAY_DIR="$(npm root -g)/@rcodex-lab/gateway"
if [ ! -d "$GATEWAY_DIR" ]; then
  echo "error: gateway not found at $GATEWAY_DIR" >&2
  exit 1
fi

echo "==> applying patches to $GATEWAY_DIR"
node "$SCRIPT_DIR/patches/patch-gateway.mjs" --target "$GATEWAY_DIR" --strict
node "$SCRIPT_DIR/patches/patch-gateway.mjs" --target "$GATEWAY_DIR" --verify-only --strict

if [ "${INSTALL_SYSTEMD_HOOK:-1}" = "1" ] && command -v systemctl >/dev/null 2>&1 \
   && systemctl --user show-environment >/dev/null 2>&1; then
  echo "==> installing the systemd pre-start hook"
  node "$SCRIPT_DIR/patches/install-systemd-hook.mjs" --target "$GATEWAY_DIR"
fi

cat <<'DONE'

==> done.
    start / restart the gateway:  rcodex-gateway service restart
    drop-in hook (systemd user):  ~/.config/systemd/user/rcodex-gateway.service.d/local-patches.conf
    patch backups:                ~/.local/state/rcodex-gateway-maintained/backups/
DONE
