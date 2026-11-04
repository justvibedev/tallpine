//go:build e2e

package daemon

import (
	"os"
	"time"
)

// Test-only knob: only binaries built with -tags e2e (the cli e2e suite) honour
// TALLPINE_DATADIR_LOCK_FIXED_WAIT. Shipped builds keep the 18s fixed part of
// the data-dir lock wait.
func init() {
	if d, err := time.ParseDuration(os.Getenv("TALLPINE_DATADIR_LOCK_FIXED_WAIT")); err == nil && d > 0 {
		dataDirLockFixedWait = d
	}
}
