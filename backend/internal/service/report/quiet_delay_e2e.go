//go:build e2e

package report

import (
	"os"
	"time"
)

// Test-only knob: only binaries built with -tags e2e (the cli e2e suite) honour
// TALLPINE_QUIET_REPORT_DELAY. Shipped builds ignore it.
func init() {
	if d, err := time.ParseDuration(os.Getenv("TALLPINE_QUIET_REPORT_DELAY")); err == nil && d > 0 {
		DefaultQuietDelay = d
	}
}
