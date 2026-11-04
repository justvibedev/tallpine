package project

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/gitcli"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestMain(m *testing.M) {
	ports.SetDefaultGit(gitcli.New())
	testenv.Main(m)
}
