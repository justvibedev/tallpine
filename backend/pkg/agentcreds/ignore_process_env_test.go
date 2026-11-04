package agentcreds

import (
	"context"
	"testing"
)

// A scrubbed (Security mode) launch hands the agent only its launch map. The credential check
// must not find a token that exists only in the daemon's own environment.
func TestIgnoreProcessEnvHidesDaemonCredential(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "daemon-only")
	opts := ResolveOptions{ConfigDir: t.TempDir(), GOOS: "linux"}
	if _, ok := ResolveLocal(context.Background(), ProviderFirstParty, opts.WithClaudeSettings(context.Background())); !ok {
		t.Fatal("precondition: the daemon token should resolve when process env is allowed")
	}
	opts.IgnoreProcessEnv = true
	if cred, ok := ResolveLocal(context.Background(), ProviderFirstParty, opts.WithClaudeSettings(context.Background())); ok {
		t.Fatalf("daemon-only token resolved under a scrubbed launch: %s", cred.Source)
	}
	opts.CommandEnv = map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "launch"}
	if _, ok := ResolveLocal(context.Background(), ProviderFirstParty, opts.WithClaudeSettings(context.Background())); !ok {
		t.Fatal("a token in the launch map must still resolve")
	}
}
