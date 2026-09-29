#!/bin/bash
# Prints the notes for a release: how to install it, then the features and
# fixes since the release before it.
#
#   tools/release-notes.sh 0.71.1
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
version=${1:?give the version, such as 0.71.1}
repo=$(sed -n 's/^REPO="\(.*\)"$/\1/p' "$root/install.sh")
previous=$(git -C "$root" describe --tags --abbrev=0 --match 'v*' HEAD^ 2>/dev/null || true)

cat <<NOTES
Install on a Mac, or update one, in Terminal:

\`\`\`bash
curl -fsSL https://raw.githubusercontent.com/$repo/main/install.sh | bash -s -- --release $version
\`\`\`

NOTES

if [ -z "$previous" ]; then
  features=$(git -C "$root" log --format=%s -- "$root" | grep -cE '^feat(\([^)]*\))?!?:' || true)
  fixes=$(git -C "$root" log --format=%s -- "$root" | grep -cE '^fix(\([^)]*\))?!?:' || true)
  cat <<NOTES
The first beta.
Its number counts the history: $features features and $fixes fixes went into it.
From here on every feat: commit is a new minor version and every fix: a patch.
NOTES
  exit 0
fi

list() {
  git -C "$root" log --reverse --format=%s "$previous..HEAD" -- "$root" |
    sed -nE "s/^$1(\([^)]*\))?!?: (.*)/- \2/p"
}
features=$(list feat)
fixes=$(list fix)
[ -z "$features" ] || printf '## New\n\n%s\n\n' "$features"
[ -z "$fixes" ] || printf '## Fixed\n\n%s\n' "$fixes"
