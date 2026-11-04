package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/pathx"
)

// AllowRealDataEnv opts a command run inside an agent session back into the
// human's real state. Without it, commands that would start or own the app
// refuse to (see InAgentSession).
const AllowRealDataEnv = "TALLPINE_DEV_REAL_DATA"

// agentSessionEnv are the variables a Tallpine or official Agent
// Orchestrator session gives every agent process.
var agentSessionEnv = []string{"TALLPINE_SESSION_ID", "AO_SESSION_ID"}

// InAgentSession returns the session id when the process runs inside an
// agent session and the caller has not set TALLPINE_DEV_REAL_DATA=1. Read it before
// agentlaunch.ScrubInheritedHostEnv, which removes these variables.
func InAgentSession(getenv func(string) string) (string, bool) {
	if getenv(AllowRealDataEnv) == "1" {
		return "", false
	}
	for _, name := range agentSessionEnv {
		if id := strings.TrimSpace(getenv(name)); id != "" {
			return id, true
		}
	}
	return "", false
}

// CheckDaemonIsolation refuses a daemon that an agent session starts on the
// human's real state. A session inherits TALLPINE_DATA_DIR and TALLPINE_RUN_FILE from the
// production app, so `go run ./cmd/tallpine daemon` from a worker would otherwise
// open the production store (or wait on its lock). Paths under ~/.tallpine/dev and
// anywhere outside ~/.tallpine are scratch and allowed.
func CheckDaemonIsolation(cfg Config, sessionID string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil //nolint:nilerr // without a home dir there is no real ~/.tallpine to protect
	}
	root := filepath.Join(home, brand.HomeDirName)
	dev := filepath.Join(root, "dev")
	for _, p := range []struct{ name, path string }{
		{"TALLPINE_DATA_DIR", cfg.DataDir},
		{"TALLPINE_RUN_FILE", cfg.RunFilePath},
	} {
		if pathx.Within(root, p.path) && !pathx.Within(dev, p.path) {
			return fmt.Errorf("refusing to start a daemon on the real %s (%s=%s) from inside agent session %s; "+
				"point TALLPINE_DATA_DIR, TALLPINE_RUN_FILE and TALLPINE_PORT at scratch paths (for example under %s), "+
				"or set %s=1 if you really mean the real app state",
				root, p.name, p.path, sessionID, dev, AllowRealDataEnv)
		}
	}
	return nil
}
