package skillbridge

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPruneInstalledPluginsRemovesOnlyRemovedWorktreeEntries(t *testing.T) {
	claude := t.TempDir()
	dir := filepath.Join(claude, "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "installed_plugins.json")
	body := `{"version":2,"extra":{"k":1},"plugins":{
 "swift-lsp@m":[
  {"scope":"user","installPath":"/u","version":"1"},
  {"scope":"local","projectPath":"/wt/a","installPath":"/x","version":"1"},
  {"scope":"project","projectPath":"/wt/a/sub","installPath":"/x","version":"1"},
  {"scope":"local","projectPath":"/wt/ab","installPath":"/x","version":"1"},
  {"scope":"local","projectPath":"/home/me/repo","installPath":"/x","version":"1"}]}}`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := PruneWorktreeInstalls(Options{ClaudeConfigDir: claude}, "/wt", "/wt/a")
	if err != nil || n != 2 {
		t.Fatalf("pruned %d err %v, want 2", n, err)
	}
	b, _ := os.ReadFile(file)
	got := string(b)
	for _, want := range []string{`"scope": "user"`, `/wt/ab`, `/home/me/repo`, `"extra"`} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{`"/wt/a"`, `/wt/a/sub`} {
		if strings.Contains(got, gone) {
			t.Errorf("kept %q:\n%s", gone, got)
		}
	}
	if n, err := PruneWorktreeInstalls(Options{ClaudeConfigDir: claude}, "/wt", "/wt"); n != 0 || err != nil {
		t.Fatalf("root pruned %d %v", n, err)
	}
	if n, err := PruneWorktreeInstalls(Options{ClaudeConfigDir: t.TempDir()}, "/wt", "/wt/a"); n != 0 || err != nil {
		t.Fatalf("missing file: %d %v", n, err)
	}
}

func writePlugins(t *testing.T, body string) (Options, string) {
	t.Helper()
	claude := t.TempDir()
	if err := os.MkdirAll(filepath.Join(claude, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(claude, "plugins", "installed_plugins.json")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Options{ClaudeConfigDir: claude}, file
}

func TestPruneWorktreeInstallsLeavesPathsOutsideTheManagedRoot(t *testing.T) {
	o, file := writePlugins(t, `{"plugins":{"p@m":[{"scope":"local","projectPath":"/home/me/repo"}]}}`)
	if n, err := PruneWorktreeInstalls(o, "/wt", "/home/me/repo"); n != 0 || err != nil {
		t.Fatalf("pruned %d %v", n, err)
	}
	if b, _ := os.ReadFile(file); !strings.Contains(string(b), "/home/me/repo") {
		t.Fatalf("entry outside the root removed: %s", b)
	}
}

func TestPruneStaleWorktreeInstallsDropsOnlyMissingManagedPaths(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "live")
	if err := os.Mkdir(live, 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(root, "gone")
	o, file := writePlugins(t, `{"plugins":{"p@m":[
 {"scope":"local","projectPath":"`+gone+`"},
 {"scope":"local","projectPath":"`+live+`"}],
 "q@m":[{"scope":"project","projectPath":"`+gone+`/sub"}],
 "r@m":[{"scope":"local","projectPath":"/elsewhere/missing"}]}}`)
	n, err := PruneStaleWorktreeInstalls(o, root)
	if err != nil || n != 2 {
		t.Fatalf("pruned %d %v, want 2", n, err)
	}
	b, _ := os.ReadFile(file)
	got := string(b)
	if strings.Contains(got, gone) || strings.Contains(got, `"q@m"`) || !strings.Contains(got, live) || !strings.Contains(got, "/elsewhere/missing") {
		t.Fatalf("after sweep:\n%s", got)
	}
}

func TestPruneInstallsConcurrentPrunesKeepEachOther(t *testing.T) {
	var entries []string
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		entries = append(entries, `{"scope":"local","projectPath":"/wt/`+n+`"}`)
	}
	o, file := writePlugins(t, `{"plugins":{"p@m":[`+strings.Join(entries, ",")+`,{"scope":"user"}]}}`)
	var wg sync.WaitGroup
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := PruneWorktreeInstalls(o, "/wt", "/wt/"+n); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if b, _ := os.ReadFile(file); strings.Contains(string(b), "/wt/") || !strings.Contains(string(b), `"user"`) {
		t.Fatalf("a concurrent prune was lost:\n%s", b)
	}
}
