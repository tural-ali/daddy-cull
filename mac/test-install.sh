#!/bin/bash
# Installs Daddy Cull from this checkout the way someone would, then checks it
# end to end: the services answer, Setup saves and launchd starts Cull again,
# a photo dropped into Import is filed under the day it was taken, and
# restart, update and uninstall work. The library and Import folder are made
# in a folder of their own, with one synthetic photo.
#
#   mac/test-install.sh                  install and check
#   mac/test-install.sh --with-icloud    installer options pass through
#
# DADDY_CULL_HOME, DADDY_CULL_PORT and DADDY_CULL_LABEL install it somewhere
# other than a normal install, as the installer does.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
port=${DADDY_CULL_PORT:-8830}
state=${DADDY_CULL_HOME:-$HOME/Library/Application Support/Daddy Cull}
work=$(mktemp -d -t daddy-cull-install)
library="$work/Family Photos"
import="$work/Import"
url="http://127.0.0.1:$port"
[ ! -e "$state/config.json" ] || { echo "$state already holds an install; this test needs a fresh one." >&2; exit 1; }

step() { printf '\n== %s\n' "$*"; }
setup() { curl -fsS "$url/api/setup"; }
label=${DADDY_CULL_LABEL:-app.daddycull}
pid() { launchctl print "gui/$(id -u)/$label.$1" 2>/dev/null | awk '$1 == "pid" {print $3}'; }
restarted() {
  local now
  now=$(pid "$1")
  [ -n "$now" ] && [ "$now" != "$2" ]
}
done_setup() { setup | grep -q '"done":true'; }
wait_for() {
  local what=$1 tries=$2
  shift 2
  for _ in $(seq 1 "$tries"); do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  echo "timed out waiting for $what" >&2
  tail -n 60 "$state/logs/"*.log >&2 || true
  exit 1
}

step "Install"
"$root/install.sh" --yes --no-open --library "$library" --import "$import" "$@"
daddy-cull status

step "It answers, in this version, with its tools"
view=$(setup)
grep -q '"configurable":true' <<<"$view"
grep -q "\"version\":\"$("$root/tools/version.sh")\"" <<<"$view"
for tool in exiftool ffmpeg; do grep -q "{\"name\":\"$tool\",\"found\":true" <<<"$view"; done
for flag in "$@"; do
  case "$flag" in
    --with-icloud) grep -q '{"name":"icloudpd","found":true' <<<"$view" ;;
    --with-apple-photos) grep -q '{"name":"osxphotos","found":true' <<<"$view" ;;
  esac
done
curl -fsS "$url/setup" | grep -q '<div id="root">'
echo ok

step "Setup saves, and launchd starts Cull again"
body=$(/usr/bin/python3 -c 'import json,sys; view=json.loads(sys.argv[1]); config=view["config"]; config["done"]=True; print(json.dumps({"config": config}))' "$view")
web=$(pid web) writer=$(pid writer)
curl -fsS -X POST -H 'Content-Type: application/json' -H "Origin: $url" --data "$body" "$url/api/setup" | grep -q '"restarting":true'
# Both stop to take the change up, and launchd starts each again.
wait_for "the web service to start again" 30 restarted web "$web"
wait_for "the writer to start again" 30 restarted writer "$writer"
wait_for "the new setup" 30 done_setup
echo ok

step "A photo dropped into Import is filed under its day"
# Any picture macOS ships will do, made small and given a camera's date.
picture=$(find "/System/Library/Desktop Pictures" /System/Library/CoreServices -maxdepth 2 \( -name '*.heic' -o -name '*.png' \) -size +20k 2>/dev/null | head -n 1)
sips -s format jpeg -z 32 48 "$picture" --out "$work/IMG_0001.JPG" >/dev/null
exiftool -q -overwrite_original -Make=Synthetic -Model=Test '-DateTimeOriginal=2019:08:14 10:00:00' "$work/IMG_0001.JPG"
mv "$work/IMG_0001.JPG" "$import/"
filed="$library/2019/2019-08/2019-08-14/IMG_0001.JPG"
curl -fsS -X POST -H "Origin: $url" "$url/api/intake/run" >/dev/null
wait_for "$filed" 180 test -f "$filed"
[ ! -e "$import/IMG_0001.JPG" ]
echo ok

step "Restart"
daddy-cull restart
setup >/dev/null

step "Update from the checkout, keeping the settings"
"$root/install.sh" --update --no-open
setup | grep -q '"done":true'

step "Uninstall keeps the photos and the catalogue"
daddy-cull uninstall
if curl -fsS --max-time 2 "$url/api/setup" >/dev/null 2>&1; then echo "still answering after uninstall" >&2; exit 1; fi
[ ! -e "$(brew --prefix)/bin/daddy-cull" ]
[ -f "$state/library.db" ]
[ -f "$filed" ]
rm -rf "$work"
echo
echo "install: ok"
