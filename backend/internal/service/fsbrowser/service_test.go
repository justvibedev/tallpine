package fsbrowser

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func mkdirs(t *testing.T, root string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func names(l Listing) map[string]Entry {
	m := map[string]Entry{}
	for _, e := range l.Entries {
		m[e.Name] = e
	}
	return m
}

// The home listing must not probe <protected folder>/.git (that stat is what
// makes macOS ask for Documents/Desktop/Downloads access), and must not offer
// Music, Movies, Pictures or the cloud folders at all.
func TestListHomeSkipsProtectedFolders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mkdirs(t, home, "Documents/.git", "Desktop/.git", "Downloads/.git", "Music/.git", "Movies", "Pictures",
		"Library/Mobile Documents", "Library/CloudStorage", "code/.git")

	l, err := New().List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	got := names(l)
	for _, hidden := range []string{"Music", "Movies", "Pictures"} {
		if _, ok := got[hidden]; ok {
			t.Errorf("%s must not be listed", hidden)
		}
	}
	for _, unprobed := range []string{"Documents", "Desktop", "Downloads"} {
		e, ok := got[unprobed]
		if !ok {
			t.Fatalf("%s should stay navigable", unprobed)
		}
		if e.GitRepo {
			t.Errorf("%s/.git must not be probed from the home listing", unprobed)
		}
	}
	if !got["code"].GitRepo {
		t.Error("ordinary folders are still probed for .git")
	}
}

func TestListRefusesBlockedFolders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mkdirs(t, home, "Music/x", "Library/Mobile Documents/com~apple~CloudDocs", "Library/CloudStorage/GoogleDrive", "Documents/Development")
	for _, p := range []string{"Music", "Music/x", "Library/Mobile Documents", "Library/Mobile Documents/com~apple~CloudDocs", "Library/CloudStorage", "Library/CloudStorage/GoogleDrive", "music"} {
		if _, err := New().List(context.Background(), filepath.Join(home, p)); err == nil {
			t.Errorf("%s must be refused", p)
		}
	}
	// Projects live under Documents: browsing there is still allowed, and
	// repos below it are probed.
	if _, err := New().List(context.Background(), filepath.Join(home, "Documents")); err != nil {
		t.Fatalf("Documents must stay browsable: %v", err)
	}
}
