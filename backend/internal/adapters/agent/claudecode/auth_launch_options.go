package claudecode

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/pkg/agentcreds"
)

// keychainRunnerForTest replaces the keychain helper in tests so none reads the real Keychain.
var keychainRunnerForTest func(ctx context.Context, name string, args ...string) ([]byte, error)

// launchResolveOptions describes the credential lookup for a launch in workingDir with env. A
// scrubbed launch (Security mode's env allowlist) gives the agent only env, so the lookup must not
// fall back to the daemon's own variables: that would approve a credential the agent never gets.
func launchResolveOptions(workingDir string, env map[string]string) agentcreds.ResolveOptions {
	return agentcreds.ResolveOptions{
		Runner:           keychainRunnerForTest,
		AllowKeychain:    true,
		ConfigDir:        env[claudeConfigDirEnv],
		WorkingDir:       workingDir,
		CommandEnv:       env,
		IgnoreProcessEnv: agentlaunch.Scrubbed(env),
	}
}
