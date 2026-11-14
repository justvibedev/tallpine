package session

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/gitcli"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestMain(m *testing.M) {
	// Parallel git-heavy tests starve each other under CI load; production keeps 15s.
	workspaceReviewTimeout = 2 * time.Minute
	ports.SetDefaultGit(gitcli.New())
	testenv.Main(m)
}
