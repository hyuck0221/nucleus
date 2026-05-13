#!/usr/bin/env bash
set -euo pipefail

ARCH="${1:?usage: build-dmg.sh <arch> <version> <binary> [dist-dir]}"
VERSION="${2:?usage: build-dmg.sh <arch> <version> <binary> [dist-dir]}"
BINARY="${3:?usage: build-dmg.sh <arch> <version> <binary> [dist-dir]}"
DIST_DIR="${4:-dist}"

APP_NAME="Nucleus"
WORK_DIR="$DIST_DIR/dmg-$ARCH"
APP_DIR="$WORK_DIR/$APP_NAME.app"
DMG_ROOT="$WORK_DIR/root"
DMG_PATH="$DIST_DIR/$APP_NAME-$VERSION-darwin-$ARCH.dmg"

rm -rf "$WORK_DIR" "$DMG_PATH"
mkdir -p "$APP_DIR/Contents/MacOS" "$APP_DIR/Contents/Resources" "$DMG_ROOT"

cp "$BINARY" "$APP_DIR/Contents/Resources/nucleus"
chmod +x "$APP_DIR/Contents/Resources/nucleus"
cp assets/icons/Nucleus.icns "$APP_DIR/Contents/Resources/Nucleus.icns"

cat > "$APP_DIR/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key>
  <string>Nucleus</string>
  <key>CFBundleDisplayName</key>
  <string>Nucleus</string>
  <key>CFBundleIdentifier</key>
  <string>ai.nucleus.local</string>
  <key>CFBundleVersion</key>
  <string>${VERSION#v}</string>
  <key>CFBundleShortVersionString</key>
  <string>${VERSION#v}</string>
  <key>CFBundleExecutable</key>
  <string>Nucleus</string>
  <key>CFBundleIconFile</key>
  <string>Nucleus</string>
  <key>LSMinimumSystemVersion</key>
  <string>13.0</string>
  <key>LSUIElement</key>
  <true/>
</dict>
</plist>
PLIST

cat > "$APP_DIR/Contents/MacOS/Nucleus" <<'SH'
#!/bin/sh
APP_DIR="$(cd "$(dirname "$0")/.." && pwd)"
exec "$APP_DIR/Resources/nucleus" serve
SH
chmod +x "$APP_DIR/Contents/MacOS/Nucleus"

if command -v codesign >/dev/null 2>&1; then
  codesign --force --deep --sign - "$APP_DIR" >/dev/null
fi

cp -R "$APP_DIR" "$DMG_ROOT/$APP_NAME.app"
ln -s /Applications "$DMG_ROOT/Applications"

hdiutil create \
  -volname "$APP_NAME $VERSION" \
  -srcfolder "$DMG_ROOT" \
  -ov \
  -format UDZO \
  "$DMG_PATH" >/dev/null

echo "$DMG_PATH"
