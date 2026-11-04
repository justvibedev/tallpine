package claudecode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
)

// Security mode launches the agent with a scrubbed environment. The launch auth check must resolve
// the credential from that same map, not from the daemon's own environment.
func TestResolveProviderContextIgnoresDaemonEnvWhenScrubbed(t *testing.T) {
	noKeychain(t)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "daemon-only")
	t.Setenv("HOME", t.TempDir())
	noReport := func(context.Context, string, string, map[string]string) (claudeAuthReport, bool) {
		return claudeAuthReport{}, false
	}
	cfg := t.TempDir()
	open := (&Plugin{}).resolveProviderContext(context.Background(), "claude", t.TempDir(), map[string]string{claudeConfigDirEnv: cfg}, noReport)
	if !open.found || open.credential.Source != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Fatalf("unscrubbed launch should see the daemon token, got found=%v", open.found)
	}
	scrubbed := (&Plugin{}).resolveProviderContext(context.Background(), "claude", t.TempDir(),
		map[string]string{claudeConfigDirEnv: cfg, agentlaunch.EnvScrub: "1"}, noReport)
	if scrubbed.found && scrubbed.credential.Source == "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Fatal("scrubbed launch validated a token the agent never receives")
	}
	withToken := (&Plugin{}).resolveProviderContext(context.Background(), "claude", t.TempDir(),
		map[string]string{claudeConfigDirEnv: cfg, agentlaunch.EnvScrub: "1", "CLAUDE_CODE_OAUTH_TOKEN": "kept"}, noReport)
	if !withToken.found || withToken.credential.Source != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Fatal("a token kept in the launch map must still resolve")
	}
}

// noKeychain makes the credential lookup hermetic: no test may touch the real macOS Keychain.
func noKeychain(t *testing.T) {
	t.Helper()
	keychainRunnerForTest = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("no keychain in tests")
	}
	t.Cleanup(func() { keychainRunnerForTest = nil })
}

// The captured setup-token must be injected into a scrubbed launch even when the daemon itself
// holds a native credential the agent would never receive.
func TestAugmentRuntimeEnvIgnoresDaemonCredentialWhenScrubbed(t *testing.T) {
	noKeychain(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "daemon-only")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(localOAuthTokenPath(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localOAuthTokenPath(dir), []byte("captured"), 0o600); err != nil {
		t.Fatal(err)
	}
	open := map[string]string{}
	(&Plugin{}).AugmentRuntimeEnv(open, dir)
	if _, ok := open["CLAUDE_CODE_OAUTH_TOKEN"]; ok {
		t.Fatal("unscrubbed launch should defer to the daemon credential")
	}
	scrubbed := map[string]string{agentlaunch.EnvScrub: "1"}
	(&Plugin{}).AugmentRuntimeEnv(scrubbed, dir)
	if scrubbed["CLAUDE_CODE_OAUTH_TOKEN"] != "captured" {
		t.Fatal("scrubbed launch must get the captured token")
	}
}
