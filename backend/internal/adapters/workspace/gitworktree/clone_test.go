package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// spyCloneDir replaces the clone primitive for one test and records the
// sources it was asked to clone. A nil impl reports errCloneUnsupported.
func spyCloneDir(t *testing.T, impl func(src, dst string) error) *[]string {
	t.Helper()
	var calls []string
	prev := cloneDirFunc
	cloneDirFunc = func(src, dst string) error {
		calls = append(calls, src)
		if impl == nil {
			return errCloneUnsupported
		}
		return impl(src, dst)
	}
	t.Cleanup(func() { cloneDirFunc = prev })
	return &calls
}

// physicalTempDir returns a temp dir with symlinks resolved: the symlink
// confinement check compares physical targets, and macOS /var is a symlink.
func physicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func requireFile(t *testing.T, path, want string) {
	t.Helper()
	if got, err := os.ReadFile(path); err != nil || string(got) != want {
		t.Fatalf("%s = %q, %v; want %q", path, got, err, want)
	}
}

func requireAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists, want absent: %v", path, err)
	}
}

func TestSeedEntryCloneFallsBackToCopyWhenCloneFails(t *testing.T) {
	root := physicalTempDir(t)
	src := filepath.Join(root, "main")
	dst := filepath.Join(root, "wt")
	writeTree(t, src, map[string]string{"node_modules/pkg/index.js": "module", "node_modules/top.txt": "top"})
	if err := os.Symlink("pkg/index.js", filepath.Join(src, "node_modules", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	// Covers non-darwin (stub) and darwin EXDEV/ENOTSUP alike: any clone error copies.
	calls := spyCloneDir(t, nil)

	if err := seedWorktreeIncludeEntry(context.Background(), src, filepath.Join(src, "node_modules"), filepath.Join(dst, "node_modules"), true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	requireFile(t, filepath.Join(dst, "node_modules", "pkg", "index.js"), "module")
	requireFile(t, filepath.Join(dst, "node_modules", "top.txt"), "top")
	if got, err := os.Readlink(filepath.Join(dst, "node_modules", "link")); err != nil || got != "pkg/index.js" {
		t.Fatalf("link = %q, %v", got, err)
	}
	if cloneSupported && len(*calls) == 0 {
		t.Fatal("clone was not attempted on a clone-capable platform")
	}
}

func TestSeedEntryCloneDisabledNeverClones(t *testing.T) {
	root := physicalTempDir(t)
	src := filepath.Join(root, "main")
	writeTree(t, src, map[string]string{"node_modules/a.js": "a"})
	calls := spyCloneDir(t, func(string, string) error { return errors.New("spy: clone declined") })

	if err := seedWorktreeIncludeEntry(context.Background(), src, filepath.Join(src, "node_modules"), filepath.Join(root, "wt", "node_modules"), false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("clone called with clone disabled: %v", *calls)
	}
	requireFile(t, filepath.Join(root, "wt", "node_modules", "a.js"), "a")
}

func TestSeedEntryCloneSkipsUnsafeEntries(t *testing.T) {
	outside := physicalTempDir(t)
	requireOutside := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(requireOutside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, src string)
		bad   string // path relative to the destination that must not be seeded
	}{
		{"absolute symlink", func(t *testing.T, src string) {
			if err := os.Symlink(requireOutside, filepath.Join(src, "node_modules", "pkg", "abs")); err != nil {
				t.Fatal(err)
			}
		}, "node_modules/pkg/abs"},
		{"escaping relative symlink", func(t *testing.T, src string) {
			if err := os.Symlink("../../../../escape-target", filepath.Join(src, "node_modules", "pkg", "esc")); err != nil {
				t.Fatal(err)
			}
		}, "node_modules/pkg/esc"},
		{"symlink into .git", func(t *testing.T, src string) {
			writeTree(t, src, map[string]string{".git/config": "[core]"})
			if err := os.Symlink("../../../.git/config", filepath.Join(src, "node_modules", "pkg", "gitlink")); err != nil {
				t.Fatal(err)
			}
		}, "node_modules/pkg/gitlink"},
		{"nested .git directory", func(t *testing.T, src string) {
			writeTree(t, src, map[string]string{"node_modules/pkg/.git/config": "[core]"})
		}, "node_modules/pkg/.git"},
		{"nested .tallpine directory", func(t *testing.T, src string) {
			writeTree(t, src, map[string]string{"node_modules/pkg/.tallpine/state": "x"})
		}, "node_modules/pkg/.tallpine"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := physicalTempDir(t)
			src := filepath.Join(root, "main")
			dst := filepath.Join(root, "wt")
			writeTree(t, src, map[string]string{"node_modules/pkg/index.js": "module", "node_modules/ok/index.js": "ok"})
			tc.setup(t, src)
			if err := os.Mkdir(dst, 0o755); err != nil {
				t.Fatal(err)
			}
			spyCloneDir(t, func(string, string) error { return errors.New("spy: clone declined") })

			skipped, err := scanCloneTree(context.Background(), src, filepath.Join(src, "node_modules"))
			if err != nil || !slices.Contains(skipped, filepath.FromSlash(strings.TrimPrefix(tc.bad, "node_modules/"))) {
				t.Fatalf("scanCloneTree = %v, %v; want %q reported as skipped", skipped, err, tc.bad)
			}
			if err := seedWorktreeIncludeEntry(context.Background(), src, filepath.Join(src, "node_modules"), filepath.Join(dst, "node_modules"), true); err != nil {
				t.Fatalf("seed: %v", err)
			}
			requireAbsent(t, filepath.Join(dst, filepath.FromSlash(tc.bad)))
			// The unsafe entry is skipped without discarding its siblings.
			requireFile(t, filepath.Join(dst, "node_modules", "pkg", "index.js"), "module")
			requireFile(t, filepath.Join(dst, "node_modules", "ok", "index.js"), "ok")
		})
	}
}

func TestSeedEntryCloneMissingSourceIsSkipped(t *testing.T) {
	root := physicalTempDir(t)
	calls := spyCloneDir(t, func(string, string) error { return errors.New("spy: clone declined") })

	err := seedWorktreeIncludeEntry(context.Background(), root, filepath.Join(root, "node_modules"), filepath.Join(root, "wt", "node_modules"), true)
	if err != nil {
		t.Fatalf("missing source: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("clone attempted for a missing source: %v", *calls)
	}
	requireAbsent(t, filepath.Join(root, "wt", "node_modules"))
}

func TestSeedEntryCloneMergesIntoExistingDirectoryWithoutCloning(t *testing.T) {
	root := physicalTempDir(t)
	src := filepath.Join(root, "main")
	dst := filepath.Join(root, "wt")
	writeTree(t, src, map[string]string{"build/new.txt": "new", "build/tracked.txt": "from main"})
	writeTree(t, dst, map[string]string{"build/tracked.txt": "from git"})
	calls := spyCloneDir(t, func(string, string) error { return errors.New("spy: clone declined") })

	if err := seedWorktreeIncludeEntry(context.Background(), src, filepath.Join(src, "build"), filepath.Join(dst, "build"), true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("cloned over an existing directory: %v", *calls)
	}
	requireFile(t, filepath.Join(dst, "build", "new.txt"), "new")
	requireFile(t, filepath.Join(dst, "build", "tracked.txt"), "from git")
}

func TestTryCloneAssetDirRemovesPartialDestinationOnError(t *testing.T) {
	root := physicalTempDir(t)
	src := filepath.Join(root, "main", "node_modules")
	dst := filepath.Join(root, "wt", "node_modules")
	writeTree(t, src, map[string]string{"a.js": "a"})
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	spyCloneDir(t, func(_, dst string) error {
		_ = os.Mkdir(dst, 0o755) // simulate a half-made clone
		return errors.New("boom")
	})
	if tryCloneAssetDir(context.Background(), filepath.Dir(src), src, dst) {
		t.Fatal("reported success for a failed clone")
	}
	if cloneSupported {
		requireAbsent(t, dst)
	}
}

func TestWorkspaceCreateClonesWorktreeIncludeDirectories(t *testing.T) {
	t.Parallel()

	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	writeTree(t, repo, map[string]string{
		".gitignore":                 "node_modules/\n",
		"node_modules/pkg/index.js":  "module",
		"node_modules/pkg/.git/HEAD": "ref: refs/heads/x",
		".worktreeinclude":           "node_modules/\n",
	})
	runGit(t, git, repo, "add", ".gitignore")
	runGit(t, git, repo, "commit", "-m", "ignore")
	runGit(t, git, repo, "push", "origin", "main")
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "tp/clone", FreshBranch: true, CloneWorktreeInclude: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	requireFile(t, filepath.Join(info.Path, "node_modules", "pkg", "index.js"), "module")
	// A whole-directory clone would carry this nested .git over; the guard must keep it out.
	requireAbsent(t, filepath.Join(info.Path, "node_modules", "pkg", ".git"))
	if err := ws.Destroy(context.Background(), info); err != nil {
		t.Fatalf("destroy: %v", err)
	}
}

// A dangling symlink (common under node_modules/.bin) must not veto the clone:
// the copy walk skips it, so the clone drops it instead.
func TestScanCloneTreeReportsDanglingSymlinkWithoutVeto(t *testing.T) {
	t.Parallel()

	root := physicalTempDir(t)
	src := filepath.Join(root, "main", "node_modules")
	writeTree(t, src, map[string]string{"pkg/index.js": "module"})
	if err := os.Symlink("../missing/target", filepath.Join(src, "pkg", "dangling")); err != nil {
		t.Fatal(err)
	}
	skipped, err := scanCloneTree(context.Background(), filepath.Dir(src), src)
	if err != nil {
		t.Fatalf("scanCloneTree: %v", err)
	}
	if want := []string{filepath.Join("pkg", "dangling")}; !slices.Equal(skipped, want) {
		t.Fatalf("skipped = %v, want %v", skipped, want)
	}
}

// clonefile preserves source modes; a read-only directory (a Go module cache is
// 0555) must come out owner-writable so the worktree can be removed again.
func TestFinishCloneMakesDirectoriesWritableAndDropsSkipped(t *testing.T) {
	t.Parallel()

	root := physicalTempDir(t)
	dst := filepath.Join(root, "wt", "node_modules")
	writeTree(t, dst, map[string]string{"mod/sub/file.go": "package x", "mod/bad/secret": "x"})
	for _, d := range []string{"mod/sub", "mod"} {
		if err := os.Chmod(filepath.Join(dst, filepath.FromSlash(d)), 0o555); err != nil {
			t.Fatal(err)
		}
	}
	if err := finishClone(dst, []string{filepath.Join("mod", "bad")}); err != nil {
		t.Fatalf("finishClone: %v", err)
	}
	for _, d := range []string{"mod", "mod/sub"} {
		info, err := os.Stat(filepath.Join(dst, filepath.FromSlash(d)))
		if err != nil || info.Mode().Perm()&0o200 == 0 {
			t.Fatalf("%s mode = %v, %v; want owner-writable", d, info, err)
		}
	}
	requireAbsent(t, filepath.Join(dst, "mod", "bad"))
	requireFile(t, filepath.Join(dst, "mod", "sub", "file.go"), "package x")
	if err := os.RemoveAll(filepath.Join(root, "wt")); err != nil {
		t.Fatalf("clone is not removable: %v", err)
	}
}
