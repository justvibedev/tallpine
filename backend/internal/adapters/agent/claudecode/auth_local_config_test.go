package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/pkg/agentcreds"
)

const signedInConfig = `{"oauthAccount":{"accountUuid":"acct-1","emailAddress":"a@example.com"}}`

func noEnv(string) string { return "" }

// localStatus runs rung 4 for a launch env, with a decoy signed-in device
// config in the (temp) home that the env's folder must not be confused with.
func localStatus(t *testing.T, accountDir string, accountConfig string) ports.AgentAuthStatus {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(signedInConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if accountConfig != "" {
		if err := os.WriteFile(filepath.Join(accountDir, ".claude.json"), []byte(accountConfig), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	status, err := claudeLocalAuthStatus(context.Background(), agentcreds.ResolveOptions{
		Env:        noEnv,
		CommandEnv: map[string]string{"CLAUDE_CONFIG_DIR": accountDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func TestLocalAuthStatusReadsTheEnvFolderNotTheDeviceConfig(t *testing.T) {
	if got := localStatus(t, t.TempDir(), ""); got == ports.AgentAuthStatusConfigured {
		t.Fatalf("status = %q: an empty account folder was reported from ~/.claude.json", got)
	}
}

func TestLocalAuthStatusConfiguredFromTheEnvFolder(t *testing.T) {
	if got := localStatus(t, t.TempDir(), signedInConfig); got != ports.AgentAuthStatusConfigured {
		t.Fatalf("status = %q, want configured from the account's .claude.json", got)
	}
}

func TestLocalAuthStatusWithoutEnvFolderUsesDeviceConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(signedInConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := claudeLocalAuthStatus(context.Background(), agentcreds.ResolveOptions{Env: noEnv})
	if err != nil || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("status = %q, %v; want the device config", status, err)
	}
}
