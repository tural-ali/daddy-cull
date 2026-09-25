#!/bin/bash
# Installs or updates Cull Sync, the menu-bar helper that carries culling in
# Daddy Cull across to Apple Photos, and keeps it running with a LaunchAgent.
#
# This is not usually run by hand. The Apple Photos page hands out a one-time
# command, and the server puts its own address, a fresh key and the helper's
# sources in front of this file before serving it, once. From a checkout,
# install.sh runs it with --source instead, and the key already in sync.conf is
# kept.
#
# It is written for the bash that ships with macOS (3.2), and it is safe to run
# again: that is how an update is installed.
#
# Everything happens inside main, called on the very last line, so a download
# cut short runs nothing at all.

say() { printf '%s\n' "$*"; }
fail() {
  printf '\nCull Sync was not installed: %s\n' "$*" >&2
  exit 1
}
cleanup() { if [ -n "${CULL_SYNC_WORK:-}" ]; then rm -rf "$CULL_SYNC_WORK"; fi; }

main() {
  set -euo pipefail
  # sync.conf holds the key; nothing written here is for anyone else to read.
  umask 077

  local label="net.example.cullsync"
  local app="$HOME/Applications/Cull Sync.app"
  local conf_dir="$HOME/.config/daddy-cull"
  local conf="$conf_dir/sync.conf"
  local plist="$HOME/Library/LaunchAgents/$label.plist"
  local log="$HOME/Library/Logs/Cull Sync.log"
  local uid domain from="" src again
  uid=$(id -u)
  domain="gui/$uid"

  while [ $# -gt 0 ]; do
    case "$1" in
      --source) [ $# -ge 2 ] || fail "--source needs a directory."; from="$2"; shift 2 ;;
      *) fail "unknown option $1." ;;
    esac
  done
  # A served installer carries its own sources and key; a checkout has neither.
  if declare -F cull_sync_payload >/dev/null; then
    again="copy a new command from the Apple Photos page and run it"
  elif [ -n "$from" ]; then
    again="run this again"
  else
    fail "copy the setup command from the Apple Photos page in Daddy Cull and run that."
  fi

  [ "$(uname -s)" = Darwin ] || fail "run this on the Mac that has the Photos library."
  [ "$uid" != 0 ] || fail "run it as yourself, without sudo, so Cull Sync runs as you."
  local major
  major=$(sw_vers -productVersion | cut -d. -f1)
  [ "$major" -ge 14 ] 2>/dev/null || fail "Cull Sync needs macOS 14 Sonoma or later."
  # PhotoKit and the Photos permission prompt need the logged-in desktop
  # session, which an SSH login does not have.
  launchctl print "$domain" >/dev/null 2>&1 \
    || fail "log in at the Mac's desktop, then run it in Terminal there, not over SSH."

  # The key goes in first. The server stopped accepting the previous key the
  # moment it handed this one out, so an already installed helper, which reads
  # sync.conf afresh on every connection, is back at once even if the build
  # below goes wrong.
  mkdir -p "$conf_dir"
  chmod 700 "$conf_dir"
  if [ -n "${CULL_SYNC_TOKEN:-}" ]; then
    local fresh
    fresh=$(mktemp "$conf_dir/.sync.conf.XXXXXX")
    {
      say "# Written by the Cull Sync setup command from the Apple Photos page."
      say "# Where Daddy Cull is, and the key that lets Cull Sync in. Keep it private."
      printf 'url = %s\n' "$CULL_SYNC_URL"
      printf 'token = %s\n' "$CULL_SYNC_TOKEN"
    } >"$fresh"
    chmod 600 "$fresh"
    mv -f "$fresh" "$conf"
    say "Saved the key in $conf"
  elif [ -f "$conf" ]; then
    chmod 600 "$conf"
    say "Kept $conf"
  else
    say "No $conf yet: after this, choose Set up Cull Sync on the Apple Photos page to connect it."
  fi

  # Swift comes with Apple's free Command Line Tools. macOS offers to install
  # them itself; the build can only start once they are there.
  if ! xcode-select -p >/dev/null 2>&1 || ! xcrun --find swiftc >/dev/null 2>&1; then
    xcode-select --install >/dev/null 2>&1 || true
    fail "Cull Sync is built on this Mac and needs Apple's Command Line Tools. Click Install in the window that has opened, wait for it to finish, then $again."
  fi

  CULL_SYNC_WORK=$(mktemp -d "${TMPDIR:-/tmp}/cull-sync.XXXXXX")
  trap cleanup EXIT
  if [ -z "$from" ]; then
    cull_sync_payload >"$CULL_SYNC_WORK/helper.tar.gz"
    local sum
    sum=$(shasum -a 256 "$CULL_SYNC_WORK/helper.tar.gz" | cut -d' ' -f1)
    [ "$sum" = "${CULL_SYNC_PAYLOAD_SHA256:-}" ] || fail "the download was damaged. Please $again."
    tar -xzf "$CULL_SYNC_WORK/helper.tar.gz" -C "$CULL_SYNC_WORK"
    src="$CULL_SYNC_WORK/CullSync"
  else
    src=$(cd "$from" && pwd)
  fi
  [ -f "$src/build.sh" ] || fail "no Cull Sync sources in $src."

  say "Building Cull Sync. This takes a minute or so."
  /bin/zsh "$src/build.sh" || fail "the build failed; the messages above say why. Once that is fixed, $again."

  # Stop whatever is running now: the LaunchAgent's copy, or one started by
  # hand or by an older install as a login item.
  launchctl bootout "$domain/$label" >/dev/null 2>&1 || true
  if pgrep -xq CullSync; then
    osascript -e "tell application id \"$label\" to quit" >/dev/null 2>&1 || pkill -x CullSync || true
    local waited=0
    while pgrep -xq CullSync && [ "$waited" -lt 10 ]; do
      sleep 0.5
      waited=$((waited + 1))
    done
    pkill -x CullSync >/dev/null 2>&1 || true
  fi

  mkdir -p "$HOME/Applications" "$HOME/Library/LaunchAgents" "$HOME/Library/Logs"
  rm -rf "$app"
  ditto "$src/build/Cull Sync.app" "$app"
  say "Installed $app"

  # KeepAlive restarts Cull Sync if it crashes, but not after Quit in its menu,
  # which exits cleanly: quitting it keeps it stopped until the next login.
  # The environment variable tells the app launchd is starting it, so it does
  # not offer a login item of its own as well.
  local fresh_plist
  fresh_plist=$(mktemp "$HOME/Library/LaunchAgents/.cullsync.XXXXXX")
  cat >"$fresh_plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>$label</string>
	<key>ProgramArguments</key>
	<array>
		<string>$(xml "$app/Contents/MacOS/CullSync")</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>ThrottleInterval</key>
	<integer>30</integer>
	<key>EnvironmentVariables</key>
	<dict>
		<key>CULL_SYNC_LAUNCH_AGENT</key>
		<string>1</string>
	</dict>
	<key>StandardOutPath</key>
	<string>$(xml "$log")</string>
	<key>StandardErrorPath</key>
	<string>$(xml "$log")</string>
</dict>
</plist>
EOF
  plutil -lint -s "$fresh_plist" || fail "the LaunchAgent could not be written."
  chmod 644 "$fresh_plist"
  mv -f "$fresh_plist" "$plist"

  # A job that was switched off with launchctl disable cannot be loaded, and
  # bootstrap can race the bootout above for a moment, so it is retried.
  launchctl enable "$domain/$label" >/dev/null 2>&1 || true
  local tries=0
  until launchctl bootstrap "$domain" "$plist" 2>/dev/null; do
    tries=$((tries + 1))
    [ "$tries" -lt 5 ] || fail "macOS would not start the LaunchAgent in $plist."
    sleep 1
  done

  say ""
  say "Cull Sync is installed and running in the menu bar, and starts again whenever you log in."
  say ""
  say "Next:"
  say "  1. macOS will ask whether Cull Sync may access your Photos. Click Allow,"
  say "     and choose full access if you are offered a choice."
  say "     If you do not see the question, open System Settings, Privacy & Security,"
  say "     Photos, and turn on Cull Sync."
  say "  2. Go back to the Apple Photos page in Daddy Cull. It notices Cull Sync"
  say "     within a few seconds."
  say ""
  say "To run this again later, for example after an update, get a new command from the same page."
}

# xml escapes the few characters a plist string cannot hold as they are.
xml() {
  printf '%s' "$1" | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'
}

main "$@"
