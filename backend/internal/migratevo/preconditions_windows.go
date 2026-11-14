//go:build windows

package migratevo

import "errors"

// The vo to tallpine migration targets the macOS install and is unsupported on
// Windows.
var errUnsupported = errors.New("vo migration is not supported on Windows")

// migrationSupported refuses up front so no precondition passes by default.
func migrationSupported() error { return errUnsupported }

func sigterm(pid int) error { return errUnsupported }

func pidAlive(pid int) bool { return false }

func sameVolume(a, b string) (bool, error) { return false, errUnsupported }
