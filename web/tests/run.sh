#!/bin/bash
# Runs every browser test against the built app, served by vite preview on
# 127.0.0.1:8842, a few at a time. Build first: npm run build.
#
#   tests/run.sh               every test
#   tests/run.sh setup guides  only these
#
# SHOTS=<folder> saves each test's screenshots there. The tests use Google
# Chrome, so it has to be installed.
set -euo pipefail

cd "$(dirname "$0")/.."
[ -f dist/index.html ] || { echo "Build the app first: npm run build" >&2; exit 1; }

names=("$@")
if [ $# -eq 0 ]; then
  for file in tests/*.cjs; do names+=("$(basename "$file" .cjs)"); done
fi

logs=$(mktemp -d "${TMPDIR:-/tmp}/cull-browser-tests.XXXXXX")
npx vite preview --host 127.0.0.1 --port 8842 --strictPort >"$logs/preview.log" 2>&1 &
preview=$!
trap 'kill "$preview" 2>/dev/null || true; rm -rf "$logs"' EXIT
for _ in $(seq 1 50); do
  curl -fsS -o /dev/null http://127.0.0.1:8842/ 2>/dev/null && break
  sleep 0.2
done
curl -fsS -o /dev/null http://127.0.0.1:8842/ || { cat "$logs/preview.log"; exit 1; }

export APP_URL=http://127.0.0.1:8842
run() {
  local name=$1 started=$SECONDS
  if node "tests/$name.cjs" >"$logs/$name.log" 2>&1; then
    printf 'ok    %-24s %3ss\n' "$name" $((SECONDS - started))
  else
    printf 'FAIL  %-24s %3ss\n' "$name" $((SECONDS - started))
    sed 's/^/      /' "$logs/$name.log" | tail -n 30
    touch "$logs/failed-$name"
  fi
}
export -f run
export logs
# shellcheck disable=SC2016 # expanded by the bash that xargs starts
printf '%s\n' "${names[@]}" | xargs -P "${JOBS:-4}" -I{} bash -c 'run "$1"' _ {}

failed=$(find "$logs" -name 'failed-*' | wc -l | tr -d ' ')
echo "${#names[@]} tests, $failed failed"
[ "$failed" -eq 0 ]
