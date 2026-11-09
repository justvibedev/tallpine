package permissioncap

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// linkedLayout is a recorded repo and a normal linked worktree of it.
func linkedLayout(t *testing.T) (root, repo, common, private, ws string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo = filepath.Join(root, "main")
	common = filepath.Join(repo, ".git")
	private = filepath.Join(common, "worktrees", "w")
	ws = filepath.Join(root, "ws")
	for _, d := range []string{private, ws} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+private+"\n")
	writeTestFile(t, filepath.Join(private, "commondir"), "../..\n")
	writeTestFile(t, filepath.Join(private, "gitdir"), filepath.Join(ws, ".git")+"\n")
	return root, repo, common, private, ws
}

func TestGitWritableRootsForLinkedWorktree(t *testing.T) {
	_, repo, common, private, ws := linkedLayout(t)
	got := GitWritableRoots(repo, ws)
	want := []string{private, filepath.Join(common, "objects"), filepath.Join(common, "refs"), filepath.Join(common, "logs")}
	if !slices.Equal(got, want) {
		t.Fatalf("GitWritableRoots = %v, want %v", got, want)
	}
	for _, g := range got {
		if strings.HasSuffix(g, "hooks") || g == common {
			t.Errorf("hooks/config must stay read-only: %s", g)
		}
	}
	if GitWritableRoots(repo, t.TempDir()) != nil {
		t.Error("no .git file must yield no roots")
	}
	if GitWritableRoots("", ws) != nil {
		t.Error("no recorded repo must yield no roots")
	}
	if GitWritableRoots(repo, "") != nil {
		t.Error("no workspace must yield no roots")
	}
}

// B619: nothing the agent writes in its checkout may name a writable root.
func TestGitWritableRootsRefuseACraftedLayout(t *testing.T) {
	for name, plant := range map[string]func(t *testing.T, root, common, private, ws string){
		"gitdir names a foreign dir": func(t *testing.T, root, _, _, ws string) {
			writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+filepath.Join(root, "launchagents")+"\n")
		},
		"gitdir names a look-alike outside the repo": func(t *testing.T, root, _, _, ws string) {
			fake := filepath.Join(ws, "fake", "worktrees", "w")
			if err := os.MkdirAll(fake, 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(fake, "commondir"), filepath.Join(root, "launchagents")+"\n")
			writeTestFile(t, filepath.Join(fake, "gitdir"), filepath.Join(ws, ".git")+"\n")
			writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+fake+"\n")
		},
		"gitdir names a sibling worktree": func(t *testing.T, root, common, _, ws string) {
			other := filepath.Join(common, "worktrees", "other")
			if err := os.MkdirAll(other, 0o755); err != nil {
				t.Fatal(err)
			}
			otherWS := filepath.Join(root, "other-ws")
			if err := os.MkdirAll(otherWS, 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(otherWS, ".git"), "gitdir: "+other+"\n")
			writeTestFile(t, filepath.Join(other, "gitdir"), filepath.Join(otherWS, ".git")+"\n")
			writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+other+"\n")
		},
		"planted symlink inside worktrees": func(t *testing.T, root, common, _, ws string) {
			evil := filepath.Join(common, "worktrees", "evil")
			if err := os.Symlink(filepath.Join(root, "launchagents"), evil); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+evil+"\n")
		},
		".git symlink": func(t *testing.T, _, _, private, ws string) {
			// The target holds a valid claim, so only the symlink check refuses it.
			moved := filepath.Join(ws, "real-git-file")
			if err := os.Rename(filepath.Join(ws, ".git"), moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, filepath.Join(ws, ".git")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			root, repo, common, private, ws := linkedLayout(t)
			if err := os.MkdirAll(filepath.Join(root, "launchagents"), 0o755); err != nil {
				t.Fatal(err)
			}
			plant(t, root, common, private, ws)
			if got := GitWritableRoots(repo, ws); got != nil {
				t.Fatalf("a crafted layout produced roots: %v", got)
			}
		})
	}
}

// A rewritten commondir is never read, so the roots stay the recorded repo's.
func TestGitWritableRootsIgnoreARewrittenCommondir(t *testing.T) {
	root, repo, common, private, ws := linkedLayout(t)
	foreign := filepath.Join(root, "launchagents")
	writeTestFile(t, filepath.Join(private, "commondir"), foreign+"\n")
	got := GitWritableRoots(repo, ws)
	if !slices.Contains(got, filepath.Join(common, "objects")) || slices.ContainsFunc(got, func(g string) bool { return strings.HasPrefix(g, foreign) }) {
		t.Fatalf("roots = %v", got)
	}
}

func TestCodexExtrasDropAPlantedLinkAndACraftedLayout(t *testing.T) {
	root, repo, common, _, ws := linkedLayout(t)
	home := filepath.Join(root, "home")
	victim := filepath.Join(root, "victim")
	for _, d := range []string{home, victim} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cache := filepath.Join(home, ".cache")
	if err := os.Symlink(victim, cache); err != nil {
		t.Fatal(err)
	}
	_, roots := CodexWorkspaceWriteExtras(home, repo, ws)
	if slices.Contains(roots, cache) {
		t.Errorf("a planted link stayed a writable root: %v", roots)
	}
	if !slices.Contains(roots, filepath.Join(common, "objects")) || !slices.Contains(roots, filepath.Join(home, ".npm")) {
		t.Errorf("real roots must remain: %v", roots)
	}
	writeTestFile(t, filepath.Join(ws, ".git"), "gitdir: "+victim+"\n")
	if _, roots := CodexWorkspaceWriteExtras(home, repo, ws); slices.ContainsFunc(roots, func(g string) bool { return strings.HasPrefix(g, victim) || strings.HasPrefix(g, common) }) {
		t.Errorf("a crafted .git named a root: %v", roots)
	}
}
