#!/bin/bash
# Builds the macOS release of this checkout into dist/: one tarball holding a
# universal cull (Apple silicon and Intel), the web app and the daddy-cull
# command, its SHA256SUMS, and install.sh. Run on a Mac with Go, Node and the
# Xcode command-line tools; the release workflow runs it on every tag.
#
#   tools/release.sh           builds the version tools/version.sh counts
#   tools/release.sh 0.71.1    builds it as that version
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
version=${1:-$("$root/tools/version.sh")}
version=${version#v}
name="daddy-cull-$version"
out="$root/dist"
work=$(mktemp -d -t daddy-cull-release)
trap 'rm -rf "$work"' EXIT
stage="$work/$name"
mkdir -p "$stage/bin" "$out"

node_major=$(node -p 'process.versions.node.split(".")[0]' 2>/dev/null || echo 0)
[ "$node_major" -ge 22 ] || { echo "The web app needs Node 22.12 or newer; this is $(node --version 2>/dev/null || echo none)." >&2; exit 1; }

echo "Building cull $version for Apple silicon and Intel"
for arch in arm64 amd64; do
  clang_arch=$arch
  [ "$arch" = amd64 ] && clang_arch=x86_64
  (cd "$root" && GOOS=darwin GOARCH=$arch CGO_ENABLED=1 CC="clang -arch $clang_arch" MACOSX_DEPLOYMENT_TARGET=13.0 \
    go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$work/cull-$arch" ./cmd/cull)
done
lipo -create -output "$stage/bin/cull" "$work/cull-arm64" "$work/cull-amd64"
# Signed ad hoc, as Apple silicon runs nothing unsigned.
codesign -s - --force "$stage/bin/cull"
lipo -archs "$stage/bin/cull"

echo "Building the web app"
(cd "$root/web" && npm ci --no-audit --no-fund --loglevel=error && npm run build --silent)
cp -R "$root/web/dist" "$stage/web"
cp "$root/mac/daddy-cull" "$stage/bin/daddy-cull"
chmod 755 "$stage/bin/cull" "$stage/bin/daddy-cull"
cp "$root/LICENSE" "$root/README.md" "$stage/"

tar -czf "$out/$name-macos.tar.gz" -C "$work" "$name"
cp "$root/install.sh" "$out/install.sh"
(cd "$out" && shasum -a 256 "$name-macos.tar.gz" install.sh >SHA256SUMS)
echo "Built:"
(cd "$out" && ls -l "$name-macos.tar.gz" install.sh SHA256SUMS && cat SHA256SUMS)
