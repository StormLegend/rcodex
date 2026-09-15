#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATEWAY_DIR="$SCRIPT_DIR/gateway"

if ! command -v node >/dev/null 2>&1; then
  echo "error: Node.js >= 22 is required" >&2
  exit 1
fi
NODE_MAJOR="$(node -p 'process.versions.node.split(".")[0]')"
if [ "$NODE_MAJOR" -lt 22 ]; then
  echo "error: Node.js >= 22 is required (found $(node -v))" >&2
  exit 1
fi

node "$GATEWAY_DIR/src/cli.mjs" setup --yes --install-service

if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
  systemctl --user daemon-reload
  systemctl --user enable --now rcodex-gateway.service
  systemctl --user --no-pager --full status rcodex-gateway.service || true
else
  echo "配置已生成；当前系统没有可用的 systemd 用户会话。"
  echo "手动启动: node $GATEWAY_DIR/src/cli.mjs start"
fi
