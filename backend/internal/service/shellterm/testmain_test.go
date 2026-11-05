package shellterm

import (
	"os"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

// TestMain gives the package a SHELL when the host has none (env -i), so
// OpenShellTerminal tests do not depend on the host environment.
func TestMain(m *testing.M) {
	if os.Getenv("SHELL") == "" {
		os.Setenv("SHELL", "/bin/sh")
	}
	testenv.Main(m)
}
