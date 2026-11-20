package review

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/gitcli"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestMain(m *testing.M) {
	resendSettle, resendPollStep = 0, 0
	ports.SetDefaultGit(gitcli.New())
	testenv.Main(m)
}
