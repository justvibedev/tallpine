package workspacewatch

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestMain(m *testing.M) {
	// Tests of the real FSEvents backend call watchRootFSEvents directly.
	UseFSNotifyForTest()
	testenv.Main(m)
}
