//go:build e2e

package acp

import (
	"os"
	"time"
)

// Test-only knob: only binaries built with -tags e2e honour
// TALLPINE_ACP_HANDSHAKE_TIMEOUT. Shipped builds ignore it.
func init() {
	if d, err := time.ParseDuration(os.Getenv("TALLPINE_ACP_HANDSHAKE_TIMEOUT")); err == nil && d > 0 {
		handshakeTimeout = d
		sessionNewTimeout = d
	}
}
