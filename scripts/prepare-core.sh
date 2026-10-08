#!/usr/bin/env bash
# Puts a patched copy of sing-box into third_party/sing-box.
# The patch lets the core verify servers by certificate fingerprint (`pcs`
# in Xray share links), which many subscriptions use for gRPC/TLS servers.
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION=v1.14.2
DEST=third_party/sing-box
STAMP="$DEST/.vela-patched-$VERSION"

if [ -f "$STAMP" ]; then
  exit 0
fi
rm -rf "$DEST"
mkdir -p third_party
# download outside this module so the replace directive doesn't apply
TMP=$(mktemp -d)
SRC=$(cd "$TMP" && go mod init tmp >/dev/null 2>&1 && go mod download -json "github.com/sagernet/sing-box@$VERSION" | sed -n 's/.*"Dir": "\(.*\)".*/\1/p')
rm -rf "$TMP"
if [ -z "$SRC" ] || [ ! -d "$SRC" ]; then
  echo "failed to download sing-box $VERSION" >&2
  exit 1
fi
cp -R "$SRC" "$DEST"
chmod -R u+w "$DEST"
for p in patches/sing-box-*.patch; do
  patch -d "$DEST" -p1 < "$p"
done
touch "$STAMP"
echo "patched sing-box $VERSION in $DEST"
