package catalog

import (
	"path"
	"regexp"
	"strings"
)

// The rules the old app's ArchiveShots::classify applied unattended, ported
// line for line: the graduation script on the host deletes whatever these
// match, so this copy must not be looser than the one it replaces.

// Named as a capture by the device that made it. Certain. It is matched
// against the name with ASCII letters lowered, as PHP's /i matches: Go's (?i)
// would also take "ſ" for "s", and the port must not match more than PHP did.
var screenshotNamePattern = regexp.MustCompile(`^(screen[ _-]?shot|screen[ _-]?recording|simulator[ _-]?screen[ _-]?shot)`)

// A format no camera here writes.
var screenshotOnlyExtensions = map[string]bool{"png": true}

// Sidecars are never judged on their own account; they travel with the file
// they belong to.
var sidecarExtensions = map[string]bool{"xmp": true, "aae": true, "json": true, "thm": true, "lrv": true}

// ClassifyScreenshot says why a file is a screenshot, "name" or "png", or ""
// when it is not one. It reads the name only, never the file.
func ClassifyScreenshot(filePath string) string {
	// Only "/" separates, as for PHP's basename on the host.
	base := path.Base(filePath)
	extension := asciiLower(strings.TrimPrefix(path.Ext(base), "."))
	if sidecarExtensions[extension] {
		return ""
	}
	if screenshotNamePattern.MatchString(asciiLower(base)) {
		return "name"
	}
	if screenshotOnlyExtensions[extension] {
		return "png"
	}
	return ""
}

func asciiLower(value string) string {
	b := []byte(value)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
