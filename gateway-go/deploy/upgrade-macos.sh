#!/bin/sh
# Health-gated, atomic upgrade for the independent Go gateway LaunchAgent.
# The old Node gateway is outside this layout and is never stopped here.
set -eu
if [ "$#" -lt 2 ]; then echo "usage: $0 BINARY VERSION [CONFIG] [EXPECTED_SHA256]" >&2; exit 2; fi
BIN=$1
VERSION=$2
APP_ROOT=${RCG_APP_ROOT:-"$HOME/Applications/rcodex-go"}
CONFIG=${3:-"$HOME/Library/Application Support/rcodex-go/gateway.json"}
EXPECTED_SHA256=${4:-${RCG_EXPECTED_SHA256:-}}
LABEL=${RCG_LAUNCHD_LABEL:-com.stormlegend.rcodex-go}
HEALTH_URL=${RCG_HEALTH_URL:-http://127.0.0.1:18890/healthz}
case "$VERSION" in *[!A-Za-z0-9._-]*|'') echo "invalid version" >&2; exit 2;; esac
[ -f "$BIN" ] || { echo "binary not found: $BIN" >&2; exit 2; }
if [ -n "$EXPECTED_SHA256" ]; then
  ACTUAL_SHA256=$(shasum -a 256 "$BIN" | awk '{print $1}')
  [ "$ACTUAL_SHA256" = "$EXPECTED_SHA256" ] || { echo "sha256 mismatch" >&2; exit 2; }
fi
mkdir -p "$APP_ROOT/releases"
RELEASE="$APP_ROOT/releases/$VERSION"
if [ -e "$RELEASE/rcg" ]; then echo "release already exists: $VERSION" >&2; exit 2; fi
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
launchctl kickstart -k "gui/$(id -u)/$LABEL"

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
  launchctl kickstart -k "gui/$(id -u)/$LABEL" || true
  exit 1
fi
rm -f "$APP_ROOT/.health"
echo "upgraded=$VERSION"
echo "release=$RELEASE"
