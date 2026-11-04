//go:build darwin && cgo

package workspacewatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// FSEvents watches recursively, so it also reports activity inside ignored
// trees that kqueue never registered. An install filling node_modules must
// stay silent, and must not cost one git process per file.
func TestFSEventsIgnoresChurnInIgnoredDirectories(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "tallpine@example.com")
	runGit(t, root, "config", "user.name", "Tallpine Tests")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(root, "README.md")
	if err := os.WriteFile(tracked, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", ".gitignore", "README.md")
	runGit(t, root, "commit", "-m", "initial")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes, err := watchRootFSEvents(ctx, root)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	for p := range 20 {
		dir := filepath.Join(root, "node_modules", fmt.Sprintf("pkg-%02d", p), "lib")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := range 10 {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.js", f)), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	select {
	case <-changes:
		t.Fatal("writes inside an ignored directory produced a workspace change")
	case <-time.After(500 * time.Millisecond):
	}

	if err := os.WriteFile(tracked, []byte("updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForChange(t, changes)
}

// A change to the rules un-ignores what they no longer match.
func TestFSEventsForgetsIgnoredDirectoriesWhenGitignoreChanges(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	ignore := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(ignore, []byte("build/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	build := filepath.Join(root, "build")
	if err := os.Mkdir(build, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes, err := watchRootFSEvents(ctx, root)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(build, "out.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changes:
		t.Fatal("write inside the ignored build/ produced a change")
	case <-time.After(500 * time.Millisecond):
	}
	if err := os.WriteFile(ignore, []byte("dist/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForChange(t, changes)
	drainChanges(changes)
	if err := os.WriteFile(filepath.Join(build, "out.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForChange(t, changes)
}
