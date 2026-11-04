package sessionreap

import (
	"os"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

// helperSleepEnv makes the test binary a stand-in for an agent's detached
// server: macOS hides the environment of platform binaries such as
// /bin/sleep, but not of third-party ones (node, Chrome, test binaries).
const helperSleepEnv = "SESSIONREAP_TEST_SLEEP"

func TestMain(m *testing.M) {
	if os.Getenv(helperSleepEnv) == "1" {
		time.Sleep(5 * time.Minute)
		os.Exit(0)
	}
	testenv.Main(m)
}
