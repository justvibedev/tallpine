//go:build windows

package testenv

// The reaper is unix-only: ps and signals are unavailable here.
func reapStaleE2EDaemons() {}

// pidAlive is conservative on windows: nothing is ever reaped.
func pidAlive(int) bool { return true }
