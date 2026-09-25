#!/bin/zsh
# Builds build/Cull Sync.app from the sources with the Xcode command-line tools.
# No Xcode project: one compiler call, a plist and an ad-hoc signature.
set -euo pipefail

HERE=${0:A:h}
APP="$HERE/build/Cull Sync.app"

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

xcrun swiftc -O -swift-version 6 -target "$(uname -m)-apple-macos14.0" \
  -framework AppKit -framework Photos -framework ServiceManagement \
  -o "$APP/Contents/MacOS/CullSync" \
  "$HERE"/Sources/*.swift
cp "$HERE/Info.plist" "$APP/Contents/Info.plist"
plutil -lint -s "$APP/Contents/Info.plist"

# Ad-hoc signing is enough for an app built and run on this Mac. macOS ties the
# Photos permission to the signature, so a rebuilt app may be asked again.
codesign -s - --force --deep "$APP"
echo "built $APP"
