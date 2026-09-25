#!/bin/zsh
# Installs Cull Sync from this checkout, for trying out changes to the helper
# before they are deployed.
#
# Run:  ./next/mac/CullSync/install.sh
#
# The normal way in is the one-time command on the Apple Photos page, which
# runs the same setup.sh with sources and a key from the server. This keeps the
# key already in ~/.config/daddy-cull/sync.conf; without one, set Cull Sync up
# from that page afterwards.
set -euo pipefail

HERE=${0:A:h}
exec /bin/bash "$HERE/setup.sh" --source "$HERE"
