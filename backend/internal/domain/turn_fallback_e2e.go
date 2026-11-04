//go:build e2e

package domain

import (
	"os"
	"time"
)

// Test-only knob: only binaries built with -tags e2e (the cli e2e suite) honour
// TALLPINE_TURN_FALLBACK_DELAY. Shipped builds ignore it.
func init() {
	if d, err := time.ParseDuration(os.Getenv("TALLPINE_TURN_FALLBACK_DELAY")); err == nil && d > 0 {
		TurnFallbackDelay = d
	}
}
