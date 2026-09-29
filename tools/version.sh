#!/bin/bash
# Prints the version of this checkout, counted from its commit history:
# 0.<features>.<fixes since the last feature>. Every "feat:" commit is a new
# minor version and every "fix:" after it a patch, so the first release is
# numbered as if every earlier commit had been released as it landed.
#
#   tools/version.sh          0.74.2
#   tools/version.sh --tag    v0.74.2
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
git -C "$root" rev-parse --git-dir >/dev/null 2>&1 || {
  echo dev
  exit 0
}
version=$(git -C "$root" log --reverse --format=%s -- "$root" | awk '
  /^feat(\([^)]*\))?!?:/ { minor++; patch = 0; next }
  /^fix(\([^)]*\))?!?:/ { patch++ }
  END { printf "0.%d.%d\n", minor, patch }')
if [ "${1:-}" = --tag ]; then echo "v$version"; else echo "$version"; fi
