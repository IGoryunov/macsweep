#!/bin/sh
# Builds dist/Macsweep.app: the macsweep binary wrapped in an app bundle with
# an icon. Double-clicking it starts the browser UI. Ad-hoc signed so TCC
# (Full Disk Access) can identify it consistently on this machine.
set -eu
cd "$(dirname "$0")/.."
VERSION=${VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}
APP=dist/Macsweep.app
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" dist/icon.iconset

echo "building binary ($VERSION)"
CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$APP/Contents/MacOS/macsweep" ./cmd/macsweep

echo "rendering icon"
swift packaging/icon.swift dist/icon-1024.png
for s in 16 32 128 256 512; do
  sips -z $s $s dist/icon-1024.png --out "dist/icon.iconset/icon_${s}x${s}.png" >/dev/null
  d=$((s*2))
  sips -z $d $d dist/icon-1024.png --out "dist/icon.iconset/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns dist/icon.iconset -o "$APP/Contents/Resources/macsweep.icns"

sed "s/__VERSION__/$VERSION/g" packaging/Info.plist > "$APP/Contents/Info.plist"
codesign --force --deep --sign - "$APP" 2>/dev/null || echo "warning: ad-hoc codesign failed"
echo "built $APP"
