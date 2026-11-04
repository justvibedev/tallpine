package cleanup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B537: the session manager's teardown (gitworktree Destroy) refuses any
// worktree git status reports dirty. A check that salvaged the work but left
// it in place therefore kept the worktree forever and salvaged it again on
// every hourly pass. After a passing check the worktree must be clean, the
// unpushed commit must still be on the branch, and a second check (the
// manager runs it twice per teardown, and again each pass) must not salvage
// again.
func TestSalvagingCheckLeavesTheWorktreeCleanAndSalvagesOnce(t *testing.T) {
	f := newWorktreeFixture(t)
	path := dirtyWorktree(t, f, "kept")
	salvager := NewSalvager(t.TempDir(), nil)
	rec := f.store.sessions[len(f.store.sessions)-1]
	check := SalvagingSafetyCheck(f.root, nil, salvager)
	for i := 0; i < 2; i++ {
		if reason := check(context.Background(), rec, []string{path}); reason != "" {
			t.Fatalf("pass %d kept a salvaged worktree: %q", i, reason)
		}
	}
	if status, err := runGitIn(context.Background(), path, "status", "--porcelain"); err != nil || status != "" {
		t.Fatalf("worktree still dirty after salvage (teardown would refuse it): %q, %v", status, err)
	}
	if log, err := runGitIn(context.Background(), path, "log", "-1", "--format=%s"); err != nil || !strings.Contains(log, "unpushed work") {
		t.Fatalf("branch lost its commit: %q, %v", log, err)
	}
	dir := salvaged(t, salvager.Root(), "kept") // fails on a second stamp dir
	if !exists(filepath.Join(dir, "changes.patch")) || !exists(filepath.Join(dir, "untracked.tar.gz")) {
		t.Fatal("salvage incomplete")
	}
}

// A salvage that fails (here: the salvage directory cannot be written) must
// keep the worktree and leave its changes untouched: the discard runs only
// after the snapshot was completely written and renamed into place.
func TestSalvagingCheckKeepsTheWorktreeWhenSalvageFails(t *testing.T) {
	f := newWorktreeFixture(t)
	path := dirtyWorktree(t, f, "failed")
	data := t.TempDir()
	salvager := NewSalvager(data, nil)
	if err := os.WriteFile(salvager.Root(), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := f.store.sessions[len(f.store.sessions)-1]
	if reason := SalvagingSafetyCheck(f.root, nil, salvager)(context.Background(), rec, []string{path}); !strings.Contains(reason, "could not be salvaged") {
		t.Fatalf("reason = %q, want the worktree kept because salvage failed", reason)
	}
	if status, _ := runGitIn(context.Background(), path, "status", "--porcelain"); !strings.Contains(status, "main.go") || !strings.Contains(status, "notes/") {
		t.Fatalf("failed salvage discarded work: status %q", status)
	}
}

// An untracked nested git repository survives git clean -fd, so the worktree
// would stay dirty and be salvaged on every pass: it is kept, with a reason
// naming the repository, before anything is salvaged or discarded.
func TestSalvagingCheckKeepsAnUntrackedNestedRepo(t *testing.T) {
	f := newWorktreeFixture(t)
	path := dirtyWorktree(t, f, "nested")
	nested := filepath.Join(path, "tools", "vendorrepo") // under an untracked parent
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := runGitIn(context.Background(), nested, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	salvager := NewSalvager(t.TempDir(), nil)
	rec := f.store.sessions[len(f.store.sessions)-1]
	reason := SalvagingSafetyCheck(f.root, nil, salvager)(context.Background(), rec, []string{path})
	if !strings.Contains(reason, "untracked nested git repository tools/vendorrepo/") {
		t.Fatalf("reason = %q, want the nested repo named", reason)
	}
	if exists(filepath.Join(salvager.Root(), "nested")) {
		t.Fatal("salvaged a worktree it keeps anyway")
	}
	if status, _ := runGitIn(context.Background(), path, "status", "--porcelain"); !strings.Contains(status, "main.go") {
		t.Fatalf("kept worktree lost its changes: %q", status)
	}
}
