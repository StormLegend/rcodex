#!/bin/sh
# Health-gated atomic upgrade for a Go gateway systemd service.
set -eu
if [ "$#" -lt 2 ]; then echo "usage: $0 BINARY VERSION [APP_ROOT] [EXPECTED_SHA256]" >&2; exit 2; fi
BIN=$1
VERSION=$2
APP_ROOT=${3:-"$HOME/Applications/rcodex-go"}
EXPECTED_SHA256=${4:-${RCG_EXPECTED_SHA256:-}}
SERVICE=${RCG_SYSTEMD_SERVICE:-rcodex-go.service}
CONFIG=${RCG_CONFIG:-"$APP_ROOT/gateway.json"}
HEALTH_URL=${RCG_HEALTH_URL:-http://127.0.0.1:18890/healthz}
case "$VERSION" in *[!A-Za-z0-9._-]*|'') echo "invalid version" >&2; exit 2;; esac
[ -f "$BIN" ] || { echo "binary not found: $BIN" >&2; exit 2; }
if [ -n "$EXPECTED_SHA256" ]; then
  ACTUAL_SHA256=$(sha256sum "$BIN" | awk '{print $1}')
  [ "$ACTUAL_SHA256" = "$EXPECTED_SHA256" ] || { echo "sha256 mismatch" >&2; exit 2; }
fi
mkdir -p "$APP_ROOT/releases"
RELEASE="$APP_ROOT/releases/$VERSION"
[ ! -e "$RELEASE/rcg" ] || { echo "release already exists" >&2; exit 2; }
mkdir -p "$RELEASE"
cp "$BIN" "$RELEASE/.rcg.tmp"
chmod 755 "$RELEASE/.rcg.tmp"
mv "$RELEASE/.rcg.tmp" "$RELEASE/rcg"
"$RELEASE/rcg" -config "$CONFIG" -doctor
OLD_TARGET=''
if [ -L "$APP_ROOT/rcg" ]; then
  OLD_TARGET=$(readlink "$APP_ROOT/rcg")
else
  OLD_TARGET="$APP_ROOT/releases/pre-$VERSION-$(date +%Y%m%d-%H%M%S)/rcg"
  mkdir -p "$(dirname "$OLD_TARGET")"
  cp "$APP_ROOT/rcg" "$OLD_TARGET"
fi
ln -s "releases/$VERSION/rcg" "$APP_ROOT/.rcg.next"
mv -f "$APP_ROOT/.rcg.next" "$APP_ROOT/rcg"
systemctl --user restart "$SERVICE"
ok=0
for _ in $(seq 1 20); do
  sleep 1
  if curl -fsS "$HEALTH_URL" >"$APP_ROOT/.health" 2>/dev/null && grep -q '"version":"'"$VERSION"'"' "$APP_ROOT/.health"; then
    ok=1
    break
  fi
done
if [ "$ok" != 1 ]; then
  echo "health-gated upgrade failed; rolling back" >&2
  rm -f "$APP_ROOT/rcg"
  ln -s "$OLD_TARGET" "$APP_ROOT/.rcg.rollback"
  mv -f "$APP_ROOT/.rcg.rollback" "$APP_ROOT/rcg"
  systemctl --user restart "$SERVICE" || true
  exit 1
fi
rm -f "$APP_ROOT/.health"
echo "upgraded=$VERSION"
echo "release=$RELEASE"
