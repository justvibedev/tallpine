package controllers_test

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/gitcli"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/testenv"
	"tallpine.eu/tallpine/backend/internal/workspacewatch"
)

func TestMain(m *testing.M) {
	ports.SetDefaultGit(gitcli.New())
	// These tests are about the event streams, not macOS FSEvents; see the helper.
	workspacewatch.UseFSNotifyForTest()
	testenv.Main(m)
}
