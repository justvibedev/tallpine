package binaryutil

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	"tallpine.eu/tallpine/backend/internal/hookutil"
)

// managedBinDir is the directory of Tallpine's managed harness launchers, set once
// at daemon start. Each launcher there points at the harness's current
// versioned install, so a path resolved once keeps starting whatever version
// is current, and an update never touches files a running session uses.
var managedBinDir atomic.Pointer[string]

// SetManagedBinDir points every resolver at Tallpine's managed launchers. An empty
// dir turns the lookup off.
func SetManagedBinDir(dir string) {
	if strings.TrimSpace(dir) == "" {
		managedBinDir.Store(nil)
		return
	}
	managedBinDir.Store(&dir)
}

// ManagedBinary returns Tallpine's managed launcher for the first of names that has
// one. Resolvers check it before PATH: once Tallpine manages a harness, its sessions
// run Tallpine's copy, not whichever copy PATH finds.
func ManagedBinary(names ...string) (string, bool) {
	dir := managedBinDir.Load()
	if dir == nil {
		return "", false
	}
	for _, name := range names {
		for _, candidate := range managedCandidates(*dir, name) {
			if hookutil.IsExecutableFile(candidate) {
				return candidate, true
			}
		}
	}
	return "", false
}

// ManagedCurrentDir returns the current release directory of target when
// launcher is one of Tallpine's managed launchers. A resolver that needs a file
// inside the install, not the launcher, looks there.
func ManagedCurrentDir(launcher, target string) (string, bool) {
	dir := managedBinDir.Load()
	if dir == nil || launcher == "" || target == "" {
		return "", false
	}
	launcherDir := filepath.Clean(filepath.Dir(launcher))
	binDir := filepath.Clean(*dir)
	if launcherDir != binDir && (runtime.GOOS != "windows" || !strings.EqualFold(launcherDir, binDir)) {
		return "", false
	}
	return filepath.Join(filepath.Dir(binDir), target, "current"), true
}

func managedCandidates(dir, name string) []string {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return nil
	}
	if runtime.GOOS != "windows" {
		return []string{filepath.Join(dir, name)}
	}
	// Windows launchers are .cmd shims; accept a bare name or one with any
	// executable extension.
	base := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(name, ".exe"), ".cmd"), ".bat")
	return []string{filepath.Join(dir, base+".cmd")}
}
