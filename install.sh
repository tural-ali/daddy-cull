#!/bin/bash
# Installs Daddy Cull on this Mac, with everything it needs.
#
#   curl -fsSL https://raw.githubusercontent.com/tural-ali/daddy-cull-oss/main/install.sh | bash
#
# or, from a checkout of the repository, ./install.sh, which builds it from
# the source instead of downloading a release.
#
# It checks the Mac first and changes nothing if it falls short. Then it
# installs Homebrew if it is missing, the programs Daddy Cull uses, and
# Daddy Cull itself, starts it in the background and opens the setup page.
# Running it again updates Daddy Cull and keeps its settings.
#
# Written for the bash that ships with macOS (3.2). Everything happens inside
# main, called on the very last line, so a download cut short runs nothing.

REPO="tural-ali/daddy-cull-oss"
MIN_MACOS=13
MIN_RAM_GB=4
MIN_FREE_GB=2

say() { printf '%s\n' "$*"; }
step() { printf '\n\033[1m%s\033[0m\n' "$*"; }
fail() {
  printf '\n\033[31mDaddy Cull was not installed:\033[0m %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<EOF
Installs Daddy Cull on this Mac.

  --with-icloud           install icloudpd, to download from iCloud Photos
  --without-icloud        do not, and do not ask
  --with-apple-photos     install osxphotos, to import from Apple Photos on this Mac
  --without-apple-photos  do not, and do not ask
  --library DIR           the library folder (default ~/Pictures/Daddy Cull/Library)
  --import DIR            the Import folder (default ~/Pictures/Daddy Cull/Import)
  --release VERSION       install this release, such as 0.71.1, rather than the newest
  --yes                   ask nothing; iCloud and Apple Photos stay off unless --with-…
  --no-open               do not open the browser at the end
  --update                keep everything as it is and install the new version
EOF
}

# ask asks a yes or no question in the Terminal, even when this script was
# piped into bash, and answers no when there is no one to ask.
ask() {
  local question=$1 answer=""
  if [ "$ASSUME_YES" = true ] || ! { : </dev/tty; } 2>/dev/null; then
    return 1
  fi
  printf '%s [y/N] ' "$question" >/dev/tty
  read -r answer </dev/tty || true
  case "$answer" in y | Y | yes | Yes | YES) return 0 ;; *) return 1 ;; esac
}

json_string() {
  local value=$1
  value=${value//\\/\\\\}
  value=${value//\"/\\\"}
  printf '"%s"' "$value"
}

check() {
  local ok=$1 label=$2 detail=$3
  if [ "$ok" = true ]; then
    printf '  \033[32m✓\033[0m %-22s %s\n' "$label" "$detail"
  else
    printf '  \033[31m✗\033[0m %-22s %s\n' "$label" "$detail"
    PROBLEMS=$((PROBLEMS + 1))
  fi
}

# requirements checks the Mac before anything is changed.
requirements() {
  step "Checking this Mac"
  PROBLEMS=0
  [ "$(uname -s)" = Darwin ] || fail "Daddy Cull installs on a Mac. On a server, use Docker: see docs/SERVER.md."

  local version major
  version=$(sw_vers -productVersion)
  major=${version%%.*}
  if [ "$major" -ge "$MIN_MACOS" ]; then check true macOS "$version"; else check false macOS "$version: needs macOS $MIN_MACOS Ventura or newer"; fi

  local arch
  arch=$(uname -m)
  case "$arch" in
    arm64) check true Processor "Apple silicon" ;;
    x86_64) check true Processor "Intel" ;;
    *) check false Processor "$arch is not supported" ;;
  esac

  local ram
  ram=$(($(sysctl -n hw.memsize) / 1024 / 1024 / 1024))
  if [ "$ram" -ge "$MIN_RAM_GB" ]; then check true Memory "$ram GB"; else check false Memory "$ram GB: needs $MIN_RAM_GB GB"; fi

  local free
  free=$(($(df -Pk "$HOME" | awk 'NR==2 {print $4}') / 1024 / 1024))
  if [ "$free" -ge "$MIN_FREE_GB" ]; then
    check true "Free space" "$free GB on the disk your home folder is on"
  else
    check false "Free space" "$free GB: needs $MIN_FREE_GB GB for the app and its tools, plus room for your photos"
  fi

  if [ "$(id -u)" != 0 ]; then check true User "$(id -un)"; else check false User "run it as yourself, without sudo"; fi

  if curl -fsSI --max-time 15 https://github.com >/dev/null 2>&1; then
    check true Internet "github.com answers"
  else
    check false Internet "github.com does not answer; the installer downloads from it"
  fi

  local holder
  holder=$(lsof -nP -iTCP:"$PORT" -sTCP:LISTEN -Fc 2>/dev/null | sed -n 's/^c//p' | head -n 1 || true)
  if [ -z "$holder" ] || launchctl print "gui/$(id -u)/$LABEL.web" >/dev/null 2>&1; then
    check true "Port $PORT" "free for Daddy Cull"
  else
    check false "Port $PORT" "$holder is using it; quit it, or install on another port by setting DADDY_CULL_PORT, such as $((PORT + 30))"
  fi

  if [ "$PROBLEMS" -gt 0 ]; then
    fail "this Mac does not meet the requirements above. Nothing was changed."
  fi
}

homebrew() {
  step "Homebrew"
  local prefix
  for prefix in /opt/homebrew /usr/local; do
    if [ -x "$prefix/bin/brew" ]; then BREW="$prefix/bin/brew"; fi
  done
  if [ -z "${BREW:-}" ]; then
    say "Homebrew installs the programs Daddy Cull uses. Its installer asks for your Mac password."
    { : </dev/tty; } 2>/dev/null || fail "Homebrew is missing and needs a password to install. Run this in Terminal."
    /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)" </dev/tty ||
      fail "Homebrew did not install. See https://brew.sh"
    for prefix in /opt/homebrew /usr/local; do
      if [ -x "$prefix/bin/brew" ]; then BREW="$prefix/bin/brew"; fi
    done
    [ -n "${BREW:-}" ] || fail "Homebrew did not install. See https://brew.sh"
    # A new Homebrew is not on the PATH of the next Terminal until the profile says so.
    if ! grep -qs 'brew shellenv' "$HOME/.zprofile"; then
      # shellcheck disable=SC2016 # written for the profile to expand
      printf '\neval "$(%s shellenv)"\n' "$BREW" >>"$HOME/.zprofile"
      say "Added Homebrew to ~/.zprofile, so new Terminal windows find daddy-cull."
    fi
  fi
  eval "$("$BREW" shellenv)"
  BREW_PREFIX=$("$BREW" --prefix)
  say "Homebrew $("$BREW" --version | head -n 1 | cut -d' ' -f2) at $BREW_PREFIX"
}

formulae() {
  local wanted=(exiftool ffmpeg uv) missing=() name
  if [ -n "$SOURCE" ]; then wanted+=(go node@24); fi
  for name in "${wanted[@]}"; do
    "$BREW" list --formula "$name" >/dev/null 2>&1 || missing+=("$name")
  done
  if [ "${#missing[@]}" -gt 0 ]; then
    step "Installing ${missing[*]}"
    HOMEBREW_NO_INSTALL_CLEANUP=1 HOMEBREW_NO_ENV_HINTS=1 "$BREW" install "${missing[@]}" || fail "Homebrew could not install ${missing[*]}."
  fi
  step "Programs"
  say "  exiftool  $(exiftool -ver)"
  say "  ffmpeg    $(ffmpeg -version | head -n 1 | cut -d' ' -f3)"
  say "  uv        $(uv --version | cut -d' ' -f2)"
}

# build builds Daddy Cull from this checkout into $1.
build() {
  local into=$1
  step "Building Daddy Cull $VERSION from $SOURCE"
  mkdir -p "$into/bin"
  (cd "$SOURCE" && CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$into/bin/cull" ./cmd/cull) ||
    fail "the Go build failed."
  local node="$BREW_PREFIX/opt/node@24/bin"
  (cd "$SOURCE/web" && PATH="$node:$PATH" npm ci --no-audit --no-fund --loglevel=error && PATH="$node:$PATH" npm run build --silent) ||
    fail "the web build failed."
  cp -R "$SOURCE/web/dist" "$into/web"
  cp "$SOURCE/mac/daddy-cull" "$into/bin/daddy-cull"
}

# download downloads the release $VERSION into $1 and checks it.
download() {
  local into=$1 name="daddy-cull-$VERSION-macos.tar.gz" work
  step "Downloading Daddy Cull $VERSION"
  work=$(mktemp -d -t daddy-cull)
  local base="${DADDY_CULL_RELEASES:-https://github.com/$REPO/releases/download}/v$VERSION"
  curl -fL --progress-bar "$base/$name" -o "$work/$name" || fail "could not download $base/$name"
  curl -fsSL "$base/SHA256SUMS" -o "$work/SHA256SUMS" || fail "could not download the checksums of $VERSION."
  (cd "$work" && grep " $name\$" SHA256SUMS | shasum -a 256 -c -s) || fail "the download is damaged: its checksum does not match. Try again."
  mkdir -p "$into"
  tar -xzf "$work/$name" -C "$into" --strip-components 1 || fail "could not unpack $name."
  rm -rf "$work"
  # Downloaded with curl, it carries no quarantine; this is for a copy fetched by a browser.
  xattr -dr com.apple.quarantine "$into" 2>/dev/null || true
}

newest() {
  curl -fsSL "https://api.github.com/repos/$REPO/releases?per_page=1" |
    sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p' | head -n 1
}

app() {
  local into="$STATE/app/$VERSION"
  mkdir -p "$STATE/app"
  rm -rf "$into.new"
  if [ -n "$SOURCE" ]; then build "$into.new"; else download "$into.new"; fi
  if [ ! -x "$into.new/bin/cull" ] || [ ! -f "$into.new/web/index.html" ]; then
    fail "the new version is incomplete."
  fi
  chmod 755 "$into.new/bin/cull" "$into.new/bin/daddy-cull"
  rm -rf "$into"
  mv "$into.new" "$into"
  ln -sfn "$VERSION" "$STATE/app/current"
  # Only the running version is kept.
  local old
  for old in "$STATE/app"/*; do
    case "$(basename "$old")" in current | checkout | "$VERSION") ;; *) rm -rf "$old" ;; esac
  done
  if [ -n "$SOURCE" ]; then say "$SOURCE" >"$STATE/app/checkout"; else rm -f "$STATE/app/checkout"; fi
  ln -sfn "$STATE/app/current/bin/daddy-cull" "$BREW_PREFIX/bin/daddy-cull"
  say "Daddy Cull $("$STATE/app/current/bin/cull" -version) is in $STATE/app"
}

settings() {
  step "Settings"
  umask 077
  mkdir -p "$STATE/logs"
  if [ -f "$STATE/config.json" ]; then
    say "Kept the settings in $STATE/config.json"
  else
    local library=${LIBRARY:-$HOME/Pictures/Daddy Cull/Library} import=${IMPORT:-$HOME/Pictures/Daddy Cull/Import}
    printf '{\n  "library": %s,\n  "import": %s,\n  "takeoutInbox": "",\n  "shared": false,\n  "icloud": {"on": false, "appleId": "", "since": ""},\n  "immich": {"url": "", "pathPrefix": ""},\n  "done": false\n}\n' \
      "$(json_string "$library")" "$(json_string "$import")" >"$STATE/config.json"
    say "Library        $library"
    say "Import folder  $import"
    say "Change either on the setup page, which opens at the end."
  fi
  if ! grep -qs '^CULL_BIN_KEY=.\{32,\}' "$STATE/secrets.env"; then
    { grep -vs '^CULL_BIN_KEY=' "$STATE/secrets.env" || true; say "CULL_BIN_KEY=$(openssl rand -hex 32)"; } >"$STATE/secrets.env.new"
    mv "$STATE/secrets.env.new" "$STATE/secrets.env"
  fi
  chmod 600 "$STATE/config.json" "$STATE/secrets.env"
  umask 022
}

# optional installs icloudpd and osxphotos when asked for. One already
# installed is installed again, as the pinned version may have moved on.
optional() {
  local cli="$STATE/app/current/bin/daddy-cull"
  step "Optional downloads"
  if [ -x "$STATE/tools/bin/icloudpd" ] && [ "$ICLOUD" != no ]; then ICLOUD=yes; fi
  if [ -x "$STATE/tools/bin/osxphotos" ] && [ "$APPLE" != no ]; then APPLE=yes; fi
  if [ "$ICLOUD" = ask ]; then
    say "Daddy Cull can download your iCloud Photos to this Mac every six hours, with icloudpd."
    if ask "Install icloudpd for iCloud Photos?"; then ICLOUD=yes; else ICLOUD=no; fi
  fi
  if [ "$ICLOUD" = yes ]; then "$cli" icloud install || fail "icloudpd did not install."; else say "iCloud Photos: skipped. Later: daddy-cull icloud install"; fi
  if [ "$APPLE" = ask ]; then
    say "Daddy Cull can copy the photos in Apple Photos on this Mac into its Import folder, with osxphotos."
    if ask "Install osxphotos for Apple Photos?"; then APPLE=yes; else APPLE=no; fi
  fi
  if [ "$APPLE" = yes ]; then "$cli" apple-photos install || fail "osxphotos did not install."; else say "Apple Photos: skipped. Later: daddy-cull apple-photos install"; fi
}

main() {
  set -euo pipefail
  export DADDY_CULL_HOME="${DADDY_CULL_HOME:-$HOME/Library/Application Support/Daddy Cull}"
  export DADDY_CULL_PORT="${DADDY_CULL_PORT:-8830}"
  export DADDY_CULL_LABEL="${DADDY_CULL_LABEL:-app.daddycull}"
  STATE=$DADDY_CULL_HOME PORT=$DADDY_CULL_PORT LABEL=$DADDY_CULL_LABEL
  ICLOUD=ask APPLE=ask ASSUME_YES=false OPEN=true UPDATE=false LIBRARY="" IMPORT="" VERSION=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --with-icloud) ICLOUD=yes ;;
      --without-icloud) ICLOUD=no ;;
      --with-apple-photos) APPLE=yes ;;
      --without-apple-photos) APPLE=no ;;
      --library) [ $# -ge 2 ] || fail "--library needs a folder."; LIBRARY=$2; shift ;;
      --import) [ $# -ge 2 ] || fail "--import needs a folder."; IMPORT=$2; shift ;;
      --release) [ $# -ge 2 ] || fail "--release needs a version, such as 0.71.1."; VERSION=${2#v}; shift ;;
      --yes) ASSUME_YES=true ;;
      --no-open) OPEN=false ;;
      --update) UPDATE=true ;;
      -h | --help) usage; exit 0 ;;
      *) usage >&2; fail "$1 is not an option." ;;
    esac
    shift
  done
  for folder in "$LIBRARY" "$IMPORT"; do
    case "$folder" in "" | /*) ;; *) fail "give --library and --import as full paths, starting with /." ;; esac
  done
  # An update asks nothing: it keeps what is installed.
  if [ "$UPDATE" = true ]; then ASSUME_YES=true; fi

  # From a checkout this builds what is here; piped from curl it downloads a release.
  SOURCE=""
  local here=""
  if [ -f "${BASH_SOURCE[0]:-}" ]; then here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd); fi
  if [ -n "$here" ] && [ -f "$here/go.mod" ] && [ -d "$here/cmd/cull" ] && [ -f "$here/mac/daddy-cull" ]; then
    SOURCE=$here
  fi

  say "Daddy Cull installer"
  requirements
  homebrew
  formulae
  if [ -n "$SOURCE" ]; then
    VERSION=$("$SOURCE/tools/version.sh")
  elif [ -z "$VERSION" ]; then
    VERSION=$(newest || true)
    [ -n "$VERSION" ] || fail "could not find the newest release of $REPO."
  fi
  if [ -f "$STATE/config.json" ] && launchctl print "gui/$(id -u)/$LABEL.web" >/dev/null 2>&1; then
    "$STATE/app/current/bin/daddy-cull" stop >/dev/null 2>&1 || true
  fi
  app
  settings
  optional

  step "Starting Daddy Cull"
  "$STATE/app/current/bin/daddy-cull" start

  local page=""
  [ "$(plutil -extract "done" raw -o - "$STATE/config.json" 2>/dev/null || true)" = true ] || page=setup
  step "Done"
  say "Daddy Cull runs in the background and starts with your Mac: http://127.0.0.1:$PORT/$page"
  say "daddy-cull status shows how it is doing, and daddy-cull help lists what else it does."
  if [ "$OPEN" = true ]; then open "http://127.0.0.1:$PORT/$page"; fi
}

main "$@"
