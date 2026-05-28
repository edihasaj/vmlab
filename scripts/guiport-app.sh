#!/usr/bin/env bash
# Build & sign a guiport.app bundle so guiport can own a Screen Recording grant.
#
# macOS attributes Screen Recording to the responsible process — for a CLI
# that's the terminal, not guiport, so a bare `guiport` can never hold the
# grant. Wrapping the binary in a LaunchServices-launchable .app makes guiport
# its own responsible process, so the SR grant lands on the app and persists.
#
# Usage:
#   scripts/guiport-app.sh [--bin <guiport>] [--dest <dir>] [--identity <sha1|name>] [--id <bundle-id>]
#
# Defaults: bin=$(command -v guiport), dest=/Applications (fallback ~/Applications),
#           identity="Apple Development", id=com.edihasaj.guiport
#
# After running, launch it once and toggle "guiport" ON in
# System Settings → Privacy & Security → Screen Recording.
set -euo pipefail

BIN="$(command -v guiport || true)"
DEST="/Applications"
# First valid (non-revoked) codesigning identity; override with --identity.
IDENTITY="$(security find-identity -v -p codesigning 2>/dev/null | grep -v CSSMERR | grep -iE 'Developer ID Application|Apple Development' | head -1 | awk '{print $2}')"
BUNDLE_ID="com.edihasaj.guiport"

while [ $# -gt 0 ]; do
  case "$1" in
    --bin) BIN="$2"; shift 2 ;;
    --dest) DEST="$2"; shift 2 ;;
    --identity) IDENTITY="$2"; shift 2 ;;
    --id) BUNDLE_ID="$2"; shift 2 ;;
    -h|--help) sed -n '2,18p' "$0"; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

[ -n "$BIN" ] && [ -x "$BIN" ] || { echo "guiport binary not found (pass --bin)"; exit 1; }
[ -n "$IDENTITY" ] || { echo "no codesigning identity found (pass --identity <sha1|name>)"; exit 1; }

VERSION="$("$BIN" --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || echo 0.0.0)"
APP="$DEST/guiport.app"
if ! mkdir -p "$APP/Contents/MacOS" 2>/dev/null; then
  DEST="$HOME/Applications"; APP="$DEST/guiport.app"; mkdir -p "$APP/Contents/MacOS"
  echo "note: $DEST not writable; using $APP"
fi

cp "$BIN" "$APP/Contents/MacOS/guiport"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>guiport</string>
  <key>CFBundleDisplayName</key><string>guiport</string>
  <key>CFBundleIdentifier</key><string>$BUNDLE_ID</string>
  <key>CFBundleExecutable</key><string>guiport</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleVersion</key><string>$VERSION</string>
  <key>CFBundleShortVersionString</key><string>$VERSION</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>LSUIElement</key><true/>
  <key>NSScreenCaptureUsageDescription</key><string>guiport captures the screen to verify and OCR app UI.</string>
</dict>
</plist>
PLIST

codesign --force --sign "$IDENTITY" --identifier "$BUNDLE_ID" --options runtime "$APP"
/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -f "$APP" || true

echo "built & signed: $APP (guiport $VERSION, id=$BUNDLE_ID)"
echo "next: open -a \"$APP\" --args screenshot --out /tmp/probe.png   # then toggle guiport ON in Screen Recording"
