package daemon

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type fakeAccountLister []domain.AgentAccount

func (f fakeAccountLister) ListAgentAccounts(context.Context) ([]domain.AgentAccount, error) {
	return f, nil
}

func TestPruneStalePluginInstallsSweepsClaudeAccountHomes(t *testing.T) {
	data := t.TempDir()
	home := filepath.Join(data, "agent-accounts", "claude", "a1")
	codex := filepath.Join(data, "agent-accounts", "codex", "c1")
	gone := filepath.Join(data, "worktrees", "gone")
	body := `{"plugins":{"p@m":[{"scope":"local","projectPath":"` + gone + `"}]}}`
	for _, dir := range []string{home, codex} {
		if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugins", "installed_plugins.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	inherited := t.TempDir() // an inherited CLAUDE_CONFIG_DIR must not be swept in place of ~/.claude
	t.Setenv("CLAUDE_CONFIG_DIR", inherited)
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	device := filepath.Join(userHome, ".claude")
	for _, dir := range []string{device, inherited} {
		if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugins", "installed_plugins.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	accounts := fakeAccountLister{
		{ID: "a1", Provider: domain.AgentAccountProviderClaude, Home: home},
		{ID: "c1", Provider: domain.AgentAccountProviderCodex, Home: codex},
	}
	pruneStalePluginInstalls(context.Background(), data, accounts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	read := func(dir string) string {
		b, _ := os.ReadFile(filepath.Join(dir, "plugins", "installed_plugins.json"))
		return string(b)
	}
	if strings.Contains(read(home), gone) {
		t.Errorf("claude account home not swept: %s", read(home))
	}
	if strings.Contains(read(device), gone) {
		t.Errorf("device folder not swept: %s", read(device))
	}
	if !strings.Contains(read(inherited), gone) {
		t.Errorf("inherited CLAUDE_CONFIG_DIR swept: %s", read(inherited))
	}
	if !strings.Contains(read(codex), gone) {
		t.Errorf("non-claude account touched: %s", read(codex))
	}
}
