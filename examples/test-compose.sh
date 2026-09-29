#!/usr/bin/env bash
# Starts examples/docker-compose.yml against empty folders, drops a photo into
# Import and checks it is filed under the day it was taken and shown by the web
# container. Needs only Docker and curl.
#   examples/test-compose.sh
set -euo pipefail

cd "$(dirname "$0")/.."
work=$(mktemp -d)
project=daddy-cull-test
port=${PORT:-18830}
compose=(docker compose --env-file "$work/env" -p "$project" -f examples/docker-compose.yml)

cleanup() {
	status=$?
	if [ "$status" -ne 0 ]; then
		"${compose[@]}" logs --no-color || true
	fi
	"${compose[@]}" down --remove-orphans >/dev/null 2>&1 || true
	rm -rf "$work"
	exit "$status"
}
trap cleanup EXIT

mkdir -p "$work/library" "$work/import" "$work/state"
cat >"$work/env" <<EOF
LIBRARY=$work/library
IMPORT=$work/import
STATE=$work/state
PUID=$(id -u)
PGID=$(id -g)
TZ=Europe/London
LISTEN_IP=127.0.0.1
PORT=$port
CULL_BIN_KEY=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
VERSION=test
EOF

"${compose[@]}" up -d --build --wait

# A photo taken on 14 July 2024, made with the image's own ffmpeg and exiftool.
docker run --rm --user "$(id -u):$(id -g)" -v "$work/import:/import" --entrypoint sh daddy-cull:local -c \
	'ffmpeg -loglevel error -f lavfi -i testsrc=size=640x480 -frames:v 1 /import/.IMG_0001.jpg &&
	 exiftool -q -overwrite_original -DateTimeOriginal="2024:07:14 16:20:05" /import/.IMG_0001.jpg &&
	 mv /import/.IMG_0001.jpg /import/IMG_0001.jpg'

filed="$work/library/2024/2024-07/2024-07-14/IMG_0001.jpg"
for _ in $(seq 1 60); do
	[ -f "$filed" ] && break
	sleep 3
done
[ -f "$filed" ] || { echo "IMG_0001.jpg was not filed under 2024/2024-07/2024-07-14"; exit 1; }
[ -z "$(ls -A "$work/import")" ] || { echo "Import still holds files after filing"; exit 1; }
echo "Filed into the library by the day it was taken."

api="http://127.0.0.1:$port"
for _ in $(seq 1 20); do
	curl -fsS "$api/api/stats" | grep -q '"total":1,' && break
	sleep 1
done
curl -fsS "$api/api/stats" | grep -q '"total":1,' || { echo "the web container does not count the photo"; exit 1; }
curl -fsS -o /dev/null "$api/"
curl -fsS "$api/api/setup" | grep -q '"configurable":false' || { echo "the Setup page should show the server's folders read-only"; exit 1; }
"${compose[@]}" logs --no-color web | grep -q 'Daddy Cull test listening' || { echo "the web container does not report its version"; exit 1; }
if "${compose[@]}" logs --no-color | grep -q 'could not be made'; then
	echo "a folder beside the catalogue could not be made"
	exit 1
fi
echo "The web container shows it at $api."
