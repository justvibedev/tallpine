//go:build darwin || linux

package peeradmit

import (
	"os"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestMain(m *testing.M) {
	if role := os.Getenv(helperRoleEnv); role != "" {
		runHelper(role)
		return
	}
	testenv.Main(m)
}
