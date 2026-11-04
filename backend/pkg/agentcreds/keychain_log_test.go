package agentcreds

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestKeychainMissLogsServiceNameNotSecrets(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	_, _ = ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
		Env: envFrom(nil), ConfigDir: t.TempDir(), GOOS: "darwin", AllowKeychain: true,
		CommandEnv: map[string]string{"CLAUDE_CONFIG_DIR": "/tmp/acct-b"},
		Runner: func(context.Context, string, ...string) ([]byte, error) {
			// The helper's output must never reach the log, even on failure.
			return []byte("sk-ant-oat01-SECRET"), errors.New("exit status 44")
		},
	})
	out := buf.String()
	for _, want := range []string{"keychain item not found", "Claude Code-credentials-f10c181c", "Claude Code-f10c181c"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "SECRET") {
		t.Fatalf("log leaks keychain output:\n%s", out)
	}
}
