package skillbridge

import (
	"os"
	"strings"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/nativeconfig"
)

// OptionsFor resolves the skill sources an agent launched with env sees: the
// account home (env HOME, as the child process gets it) and, for Claude, a
// project CLAUDE_CONFIG_DIR. Launch and the read-only skills API both use it so
// the page and the session agree.
func OptionsFor(dataDir, workspace string, env map[string]string) Options {
	home := strings.TrimSpace(env["HOME"])
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	claude, _ := nativeconfig.Resolve(env, "CLAUDE_CONFIG_DIR", ".claude")
	return Options{Workspace: workspace, Home: home, ClaudeConfigDir: claude, DataDir: dataDir}
}
