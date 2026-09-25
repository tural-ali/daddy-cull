// Package mac carries the Cull Sync helper's sources inside the preview's
// binary. A Mac is set up by running one command that fetches an installer
// from the preview, and the helper is built on the Mac from these exact files,
// so the helper a Mac runs always matches the server it talks to and no
// prebuilt binary is kept anywhere.
package mac

import "embed"

// CullSync holds setup.sh, the installer the preview serves, and what it
// builds the app from. The build directory and the tests are left out: only
// what the Mac needs to compile the app travels.
//
//go:embed CullSync/setup.sh CullSync/build.sh CullSync/Info.plist CullSync/Sources/*.swift
var CullSync embed.FS
