// Package version exposes the temporal binary's compile-time version.
//
// The source of truth is internal/version/VERSION, embedded into the
// binary via //go:embed. Tag is its trimmed contents.
//
// The file lives next to this package (rather than at the repo root)
// because //go:embed only accepts patterns within the package
// subtree. scripts/check-version.sh and tests reach for it via
// internal/version/VERSION.
//
// There is no -ldflags wiring: every build path (`go build`,
// `make build`, `go install`, CI release) reads the same value from
// the embedded file, so a developer who runs `go build .` directly
// gets the same version a release binary would.
package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var versionFile string

// Tag is the human-readable build tag, e.g. "v0.1.0", read from the
// embedded VERSION file with surrounding whitespace stripped.
var Tag = strings.TrimSpace(versionFile)

// String returns the version string printed by `temporal version`.
func String() string {
	return Tag
}
