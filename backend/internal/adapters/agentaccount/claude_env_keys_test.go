package agentaccount

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/pricing"
)

func TestClaudeLaunchEnvKeysMatchLaunchEnv(t *testing.T) {
	// LaunchEnv neutralises a credential variable only when the daemon has it
	// set, so set them all to see every key it can emit.
	for _, key := range pricing.ClaudeCredentialEnv {
		t.Setenv(key, "set")
	}
	env := (&Claude{}).LaunchEnv(t.TempDir())
	listed := map[string]bool{}
	for _, key := range pricing.ClaudeLaunchEnvKeys() {
		listed[key] = true
		if _, ok := env[key]; !ok {
			t.Errorf("ClaudeLaunchEnvKeys lists %s, LaunchEnv never sets it", key)
		}
	}
	for key := range env {
		if !listed[key] {
			t.Errorf("LaunchEnv sets %s, missing from ClaudeLaunchEnvKeys", key)
		}
	}
}
