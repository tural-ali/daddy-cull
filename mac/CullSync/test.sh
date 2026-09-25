#!/bin/zsh
# Compiles the pure matching logic with its tests and runs them. Nothing here
# touches the Photos library or the network.
set -euo pipefail

HERE=${0:A:h}
OUT="$HERE/build/tests"
mkdir -p "$HERE/build"

xcrun swiftc -swift-version 6 -target "$(uname -m)-apple-macos14.0" \
  -o "$OUT" \
  "$HERE/Sources/Matching.swift" "$HERE/Sources/Config.swift" "$HERE/Tests/main.swift"
"$OUT"
