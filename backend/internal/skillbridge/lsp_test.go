package skillbridge

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMainLanguagesPicksDominantAndSkipsVendored(t *testing.T) {
	var paths []string
	add := func(prefix, ext string, n int) {
		for i := 0; i < n; i++ {
			paths = append(paths, prefix+string(rune('a'+i))+ext)
		}
	}
	add("backend/", ".go", 20)
	add("frontend/src/", ".tsx", 12)
	add("node_modules/x/", ".js", 500) // vendored: ignored
	add("scripts/", ".py", 2)          // below the minimum
	paths = append(paths, "README.md", "Makefile")
	got := MainLanguages(CountLanguages(paths))
	want := []LangCount{{"go", 20}, {"typescript", 12}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	// A language under the share threshold is dropped even with enough files.
	if got := MainLanguages(map[string]int{"go": 100, "swift": 6}); len(got) != 1 || got[0].Language != "go" {
		t.Fatalf("share threshold: %+v", got)
	}
}

func TestDetectLanguagesReadsTrackedFiles(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	for i := 0; i < 6; i++ {
		write(t, filepath.Join(dir, "pkg", "f"+string(rune('a'+i))+".go"), "package p\n")
	}
	write(t, filepath.Join(dir, "untracked.py"), "x")
	git(t, dir, "add", "pkg")
	got := DetectLanguages(dir)
	if len(got) != 1 || got[0] != (LangCount{"go", 6}) {
		t.Fatalf("got %+v", got)
	}
	if got := DetectLanguages(t.TempDir()); got != nil {
		t.Fatalf("non-repo: %+v", got)
	}
}

func TestBinaryInstalledFindsToolDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	if BinaryInstalled(home, "zz-fake-lsp") {
		t.Fatal("missing binary reported installed")
	}
	bin := filepath.Join(home, "go", "bin", "zz-fake-lsp")
	write(t, bin, "#!/bin/sh\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if !BinaryInstalled(home, "zz-fake-lsp") {
		t.Fatal("binary in ~/go/bin not found")
	}
	pathDir := t.TempDir()
	write(t, filepath.Join(pathDir, "zz-on-path"), "#!/bin/sh\n")
	_ = os.Chmod(filepath.Join(pathDir, "zz-on-path"), 0o755)
	t.Setenv("PATH", pathDir)
	if !BinaryInstalled(home, "zz-on-path") {
		t.Fatal("binary on PATH not found")
	}
}

func TestBinaryProblem(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	have := map[string]string{}
	prev := lookTool
	lookTool = func(_, name string) (string, bool) { p, ok := have[name]; return p, ok }
	t.Cleanup(func() { lookTool = prev })
	gopls, _ := LSPByLanguage("go")
	ts, _ := LSPByLanguage("typescript")

	if BinaryProblem(home, gopls) != "" {
		t.Fatal("a missing binary is BinaryInstalled's business, not a problem")
	}
	have["gopls"] = "/x/gopls"
	if p := BinaryProblem(home, gopls); p != LSPProblemNoGo {
		t.Fatalf("gopls without go: %q", p)
	}
	have["go"] = "/x/go"
	if p := BinaryProblem(home, gopls); p != "" {
		t.Fatalf("gopls with go: %q", p)
	}

	nm := filepath.Join(root, "lib", "node_modules")
	cli := filepath.Join(nm, "typescript-language-server", "lib", "cli.mjs")
	write(t, cli, "")
	write(t, filepath.Join(nm, "typescript", "package.json"), "{}")
	have["typescript-language-server"] = cli
	if p := BinaryProblem(home, ts); p != LSPProblemNoTSServer {
		t.Fatalf("typescript without tsserver.js: %q", p)
	}
	write(t, filepath.Join(nm, "typescript", "lib", "tsserver.js"), "")
	if p := BinaryProblem(home, ts); p != "" {
		t.Fatalf("typescript with tsserver.js: %q", p)
	}
}

func TestTypeScriptCatalogPinsTypeScript5(t *testing.T) {
	ts, _ := LSPByLanguage("typescript")
	if got := ts.BinaryCommands[0].Args; got[len(got)-1] != "typescript@5" || !strings.Contains(ts.BinaryInstall, "typescript@5") {
		t.Fatalf("typescript must be pinned: %v / %q", got, ts.BinaryInstall)
	}
}
