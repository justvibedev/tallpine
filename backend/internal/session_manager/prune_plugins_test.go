package sessionmanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestCleanupSessionPrunesInstalledPluginsOfTheRemovedWorktree(t *testing.T) {
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	if err := os.MkdirAll(filepath.Join(claude, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(claude, "plugins", "installed_plugins.json")
	m, st, _, _ := newManager()
	m.dataDir = t.TempDir()
	root := filepath.Join(m.dataDir, "worktrees")
	body := `{"version":2,"plugins":{"swift-lsp@m":[
 {"scope":"local","projectPath":"` + root + `/mer-1","installPath":"/x"},
 {"scope":"local","projectPath":"` + root + `/mer-2","installPath":"/x"},
 {"scope":"local","projectPath":"/ws/mer-3","installPath":"/x"},
 {"scope":"user","installPath":"/u"}]}}`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for id, path := range map[domain.SessionID]string{"mer-1": root + "/mer-1", "mer-3": "/ws/mer-3"} {
		seedTerminal(st, id, domain.SessionMetadata{WorkspacePath: path})
		rec := st.sessions[id]
		rec.Harness = domain.HarnessClaudeCode
		st.sessions[id] = rec
		if _, err := m.CleanupSession(ctx, id, nil); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(file)
	got := string(b)
	if strings.Contains(got, root+"/mer-1") || !strings.Contains(got, root+"/mer-2") || !strings.Contains(got, "/ws/mer-3") || !strings.Contains(got, `"user"`) {
		t.Fatalf("installed_plugins.json after cleanup:\n%s", got)
	}
}
