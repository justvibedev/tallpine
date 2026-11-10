// Package buildinfo holds the build metadata stamped into the tallpine binary. The
// build scripts (frontend/scripts/build-daemon.mjs, scripts/daemon-build.sh)
// set these with -ldflags "-X .../internal/buildinfo.<Name>=<value>", so the
// CLI's `tallpine --version`, the daemon's /healthz payload and its startup log line
// all report the exact commit a binary was built from.
package buildinfo

import "strings"

// Build metadata. Release tooling overrides these with -ldflags.
var (
	// Version is the release version, e.g. "0.13.0".
	Version = "dev"
	// Commit is the full git commit SHA the binary was built from.
	Commit = ""
	// Date is the UTC build time in RFC 3339 form.
	Date = ""
	// Dirty is "true" when the work tree had uncommitted changes at build time.
	Dirty = ""
	// TeamID is the Apple Team ID a signed release build is signed by. Empty
	// on unsigned, ad-hoc and development builds. It is fixed at build time:
	// nothing at runtime can set it.
	TeamID = ""
)

// IsDirty reports whether the binary was built from a dirty work tree.
func IsDirty() bool { return Dirty == "true" }

// String renders the metadata as "<version> commit <sha> built <date>
// (dirty)", omitting the parts that are unset.
func String() string {
	parts := []string{Version}
	if Commit != "" {
		parts = append(parts, "commit "+Commit)
	}
	if Date != "" {
		parts = append(parts, "built "+Date)
	}
	if IsDirty() {
		parts = append(parts, "(dirty)")
	}
	return strings.Join(parts, " ")
}

// Fields returns the metadata as a JSON-friendly map for probes and logs.
func Fields() map[string]any {
	return map[string]any{
		"version":   Version,
		"commit":    Commit,
		"buildDate": Date,
		"dirty":     IsDirty(),
	}
}
