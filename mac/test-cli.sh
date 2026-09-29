#!/bin/bash
# Tests the daddy-cull command with stand-ins for icloudpd and osxphotos, in a
# state folder of its own. Nothing here signs in anywhere, reads Photos or
# starts a service. Every account and folder is a synthetic fixture.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
CLI="$HERE/daddy-cull"
WORK=$(mktemp -d -t daddy-cull-test)
trap 'rm -rf "$WORK"' EXIT
export DADDY_CULL_HOME="$WORK/state" DADDY_CULL_LABEL="app.daddycull.test-$$" DADDY_CULL_PORT=18830
STATE=$DADDY_CULL_HOME
BIN="$STATE/tools/bin"
mkdir -p "$BIN" "$WORK/home"

failures=0
pass() { printf '  ok    %s\n' "$1"; }
fail() {
  printf '  FAIL  %s\n' "$1"
  failures=$((failures + 1))
}
expect() {
  local name=$1 want=$2 got=$3
  if [ "$got" = "$want" ]; then pass "$name"; else fail "$name: want [$want], got [$got]"; fi
}
has() {
  local name=$1 want=$2 got=$3
  case "$got" in *"$want"*) pass "$name" ;; *) fail "$name: [$want] not in [$got]" ;; esac
}
lacks() {
  local name=$1 want=$2 got=$3
  case "$got" in *"$want"*) fail "$name: [$want] in [$got]" ;; *) pass "$name" ;; esac
}
holds() {
  local name=$1
  shift
  if "$@"; then pass "$name"; else fail "$name"; fi
}
field() { plutil -extract "$2" raw -o - "$1" 2>/dev/null || true; }

config() {
  local on=$1 since=${2:-}
  cat >"$STATE/config.json" <<EOF
{"library":"$WORK/home/Library","import":"$WORK/home/Import","takeoutInbox":"","shared":false,
 "icloud":{"on":$on,"appleId":"sam@example.com","since":"$since"},"immich":{"url":"","pathPrefix":""},"done":true}
EOF
}

# The stand-in icloudpd records its arguments and does what FAKE says:
# download N files, ask for a new sign-in, or fail.
cat >"$BIN/icloudpd" <<'EOF'
#!/bin/bash
printf '%s\n' "$@" >"$DADDY_CULL_HOME/icloudpd.args"
hook=""
while [ $# -gt 0 ]; do
  if [ "$1" = --notification-script ]; then hook=$2; fi
  shift
done
case "${FAKE:-0}" in
  mfa) "$hook"; echo "ERROR Two-factor authentication is required"; exit 1 ;;
  broken) echo "INFO Downloaded /x/IMG_0001.HEIC"; echo "ERROR something else"; exit 2 ;;
  *)
    i=0
    while [ "$i" -lt "${FAKE:-0}" ]; do
      i=$((i + 1))
      echo "2026-09-29 INFO Downloaded /x/IMG_000$i.HEIC"
    done
    echo "INFO All photos and videos have been downloaded"
    ;;
esac
EOF
cat >"$BIN/osxphotos" <<'EOF'
#!/bin/bash
printf '%s\n' "$@" >"$DADDY_CULL_HOME/osxphotos.args"
[ "${FAKE:-}" = broken ] && { echo "Error: Photos library locked"; exit 1; }
echo "Exporting 5 photos to $2..."
echo "Processed: 5 photos, exported: ${FAKE:-5}, updated: 0, skipped: 0, updated EXIF data: 0, missing: 0, error: 0, touched date: 5"
EOF
chmod +x "$BIN/icloudpd" "$BIN/osxphotos"
status="$STATE/icloud-status.json"

echo "iCloud"
config false
out=$("$CLI" icloud run 2>&1)
has "off: says how to turn it on" "Turn them on in setup" "$out"
holds "off: records nothing" [ ! -f "$status" ]

config true 2024-01-01
if "$CLI" icloud run >/dev/null 2>&1; then fail "not signed in: a run from Terminal fails"; else pass "not signed in: a run from Terminal fails"; fi
holds "not signed in: a scheduled run is quiet" "$CLI" icloud run --scheduled
holds "not signed in: records nothing" [ ! -f "$status" ]
holds "not signed in: icloudpd is not run" [ ! -f "$STATE/icloudpd.args" ]

FAKE=0 "$CLI" icloud sign-in >/dev/null
args=$(cat "$STATE/icloudpd.args")
has "sign-in: only signs in" "--auth-only" "$args"
has "sign-in: asks in Terminal, keeps it in the keychain" "--password-provider
keyring
--password-provider
console" "$args"
expect "sign-in: recorded" "true" "$(field "$status" signedIn)"
expect "sign-in: session folder is private" "700" "$(stat -f %Lp "$STATE/icloud-session")"
touch "$STATE/icloud-session/session"

FAKE=3 "$CLI" icloud run >/dev/null
args=$(cat "$STATE/icloudpd.args")
expect "first run: counts downloads" "Downloaded 3 new photos and videos." "$(field "$status" message)"
expect "first run: complete" "true" "$(field "$status" complete)"
has "first run: from the chosen day" "--skip-created-before
2024-01-01" "$args"
lacks "first run: goes through the whole library" "--until-found" "$args"
lacks "scheduled runs never ask for a password" "console" "$args"
has "one flat folder, filed by the writer" "--folder-structure
none" "$args"
has "into the state folder" "$STATE/iCloud" "$args"

FAKE=0 "$CLI" icloud run --scheduled >/dev/null
expect "next run: nothing new" "Nothing new to download." "$(field "$status" message)"
has "next run: stops at what is already here" "--until-found
100" "$(cat "$STATE/icloudpd.args")"
FAKE=1 "$CLI" icloud run >/dev/null
expect "one download" "Downloaded 1 new photo or video." "$(field "$status" message)"

FAKE=mfa "$CLI" icloud run --scheduled >/dev/null
expect "Apple asks again: signed out" "false" "$(field "$status" signedIn)"
has "Apple asks again: says what to run" "daddy-cull icloud sign-in" "$(field "$status" message)"
expect "Apple asks again: still complete" "true" "$(field "$status" complete)"
holds "Apple asks again: marker cleared" [ ! -e "$STATE/icloud-needs-sign-in" ]

FAKE=broken "$CLI" icloud run >/dev/null
expect "error: not ok" "false" "$(field "$status" ok)"
expect "error: says how far it got" "The download stopped with an error after 1 new photos and videos. See daddy-cull logs." "$(field "$status" message)"

mkdir "$STATE/icloud.lock"
sleep 30 &
sleeper=$!
echo "$sleeper" >"$STATE/icloud.lock/pid"
rm -f "$STATE/icloudpd.args"
out=$(FAKE=5 "$CLI" icloud run)
has "a run already going: says so" "already running" "$out"
holds "a run already going: not run twice" [ ! -f "$STATE/icloudpd.args" ]
kill "$sleeper"
wait "$sleeper" 2>/dev/null || true
FAKE=2 "$CLI" icloud run >/dev/null
expect "a stale lock is taken over" "Downloaded 2 new photos and videos." "$(field "$status" message)"
holds "the lock is let go" [ ! -d "$STATE/icloud.lock" ]

echo "Apple Photos"
photos="$STATE/apple-photos-status.json"
FAKE=5 "$CLI" apple-photos import --from-date 2024-01-01 >/dev/null
args=$(cat "$STATE/osxphotos.args")
holds "exports into Import/Apple Photos" [ -d "$WORK/home/Import/Apple Photos" ]
has "only what was not exported before" "--update
--only-new
--exportdb
$STATE/osxphotos.db" "$args"
has "extra options pass through" "--from-date
2024-01-01" "$args"
expect "counts exports" "Exported 5 new photos and videos." "$(field "$photos" message)"
FAKE=0 "$CLI" apple-photos import >/dev/null
expect "nothing new" "Nothing new in Apple Photos." "$(field "$photos" message)"
if FAKE=broken "$CLI" apple-photos import >/dev/null 2>&1; then fail "an error fails"; else pass "an error fails"; fi
expect "an error is recorded" "false" "$(field "$photos" ok)"

echo "Status"
out=$("$CLI" status)
has "status: folders" "$WORK/home/Library" "$out"
has "status: last iCloud run" "Downloaded 2 new photos and videos." "$out"
has "status: services stopped" "stopped" "$out"
out=$("$CLI" help)
has "help lists sign-in" "daddy-cull icloud sign-in" "$out"
if "$CLI" nonsense >/dev/null 2>&1; then fail "an unknown command fails"; else pass "an unknown command fails"; fi

if [ "$failures" -gt 0 ]; then
  echo "$failures failed"
  exit 1
fi
echo "daddy-cull: ok"
