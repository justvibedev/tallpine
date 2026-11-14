package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B686: an unnamed screenshot must land in the temp dir, not the cwd (the repo).
func TestBrowserScreenshotDefaultPathIsTempDirNotCwd(t *testing.T) {
	setBrowserIdentity(t)
	cfg := setConfigEnv(t)
	capture := &browserRequestCapture{}
	srv := browserCLIServer(t, capture)
	writeRunFileFor(t, cfg, srv)
	deps := Deps{ProcessAlive: func(int) bool { return true }}
	cwd := t.TempDir()
	t.Chdir(cwd)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	out, errOut, err := executeCLI(t, deps, "browser", "screenshot", "--json")
	if err != nil {
		t.Fatalf("default screenshot err=%v stderr=%s", err, errOut)
	}
	var metadata browserScreenshotFileResult
	if err := json.Unmarshal([]byte(out), &metadata); err != nil {
		t.Fatalf("decode JSON screenshot output: %v; output=%q", err, out)
	}
	if filepath.Dir(metadata.Path) != tmp || !strings.HasPrefix(filepath.Base(metadata.Path), "tallpine-browser-") {
		t.Fatalf("default screenshot path = %q, want tallpine-browser-*.png in %q", metadata.Path, tmp)
	}
	if entries, _ := os.ReadDir(cwd); len(entries) != 0 {
		t.Fatalf("default screenshot wrote into cwd: %v", entries)
	}
}
