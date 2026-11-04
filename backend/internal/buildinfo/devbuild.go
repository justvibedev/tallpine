package buildinfo

// IsDevBuild reports whether this binary is an unstamped development or test
// build. Release tooling always stamps Version, so test-only escape hatches
// gate on this and stay inert in shipped binaries.
func IsDevBuild() bool { return Version == "dev" }
