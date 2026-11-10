package daemon

import (
	"os"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/runtime/conpty"
	"tallpine.eu/tallpine/backend/internal/testenv"
)

// TestMain lets the direct-runtime test re-exec this binary through the same
// hidden pty-host entrypoint the production binary uses.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "pty-host" {
		os.Exit(conpty.RunHost(os.Args[2:], os.Stdout))
	}
	testenv.Main(m)
}
