package cleanup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// dirtyWorktree adds a real git worktree for session id under the fixture
// root holding: a modified tracked file, a staged new file, an untracked
// note, rebuilt output (node_modules), a symlink and an unpushed commit.
func dirtyWorktree(t *testing.T, f *worktreeFixture, id domain.SessionID) string {
	t.Helper()
	g := newInspectRepo(t)
	if err := os.WriteFile(filepath.Join(g.repo, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g.git(g.repo, "add", "main.go")
	g.git(g.repo, "commit", "-q", "-m", "main")
	g.git(g.repo, "push", "-q", "origin", "main")
	path := filepath.Join(f.root, "mer", string(id))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	g.git(g.repo, "worktree", "add", "-q", "-b", "tp/"+string(id), path)
	write := func(rel, body string) {
		full := filepath.Join(path, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("committed.txt", "unpushed\n")
	g.git(path, "add", "committed.txt")
	g.git(path, "commit", "-q", "-m", "unpushed work")
	write("main.go", "package main // edited\n")
	write("staged.go", "package main // staged\n")
	g.git(path, "add", "staged.go")
	write("notes/todo.md", "remember this\n")
	write("node_modules/dep/index.js", "rebuilt\n")
	write("internal/build/foo.go", "package build\n")
	if err := os.Symlink("/etc/hosts", filepath.Join(path, "link")); err != nil {
		t.Fatal(err)
	}
	f.paths[id] = path
	rec := domain.SessionRecord{ID: id, ProjectID: "mer", IsTerminated: true, UpdatedAt: f.now.Add(-10 * 24 * time.Hour)}
	rec.Metadata.WorkspacePath = path
	f.store.sessions = append(f.store.sessions, rec)
	return path
}

func salvaged(t *testing.T, root string, id domain.SessionID) string {
	t.Helper()
	stamps, _ := os.ReadDir(filepath.Join(root, string(id)))
	if len(stamps) != 1 {
		t.Fatalf("salvage for %s: %d stamp dirs, want 1", id, len(stamps))
	}
	return filepath.Join(root, string(id), stamps[0].Name(), "workspace")
}

func tarNames(t *testing.T, path string) []string {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	gz, err := gzip.NewReader(fh)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	sort.Strings(names)
	return names
}

func TestWorktreeRuleSalvagesAndRemovesADirtyWorktree(t *testing.T) {
	f := newWorktreeFixture(t)
	path := dirtyWorktree(t, f, "dirty")
	data := t.TempDir()
	salvager := NewSalvager(data, func(context.Context, domain.SessionID) ([]domain.PullRequest, error) {
		return []domain.PullRequest{{URL: "https://example.com/pr/1", Merged: true}}, nil
	})
	rule := NewWorktreeRule(f.root, f.remover, nil)
	rule.SetSalvager(salvager)
	report := f.runWith(rule, RunRequest{})
	if item := itemFor(t, report, "dirty"); item.Status != StatusFreed {
		t.Fatalf("item = %+v, want freed", item)
	}
	if exists(path) {
		t.Fatal("dirty worktree was not removed")
	}
	dir := salvaged(t, salvager.Root(), "dirty")
	patch, err := os.ReadFile(filepath.Join(dir, "changes.patch"))
	if err != nil || !strings.Contains(string(patch), "// edited") || !strings.Contains(string(patch), "// staged") {
		t.Fatalf("patch = %q, %v; want the unstaged and staged changes", patch, err)
	}
	if got := tarNames(t, filepath.Join(dir, "untracked.tar.gz")); len(got) != 2 || got[0] != "internal/build/foo.go" || got[1] != "notes/todo.md" {
		t.Fatalf("untracked archive = %v, want internal/build/foo.go and notes/todo.md", got)
	}
	if !exists(filepath.Join(dir, "commits.bundle")) {
		t.Fatal("unpushed commit not bundled")
	}
	readme, _ := os.ReadFile(filepath.Join(dir, "README.md"))
	for _, want := range []string{"tp/dirty", "https://example.com/pr/1 (merged)", "node_modules/"} {
		if !strings.Contains(string(readme), want) {
			t.Fatalf("README lacks %q:\n%s", want, readme)
		}
	}
}

func TestWorktreeRuleKeepsADirtyWorktreeWithAnOpenPR(t *testing.T) {
	f := newWorktreeFixture(t)
	path := dirtyWorktree(t, f, "open")
	salvager := NewSalvager(t.TempDir(), func(context.Context, domain.SessionID) ([]domain.PullRequest, error) {
		return []domain.PullRequest{{URL: "u", Merged: false}}, nil
	})
	rule := NewWorktreeRule(f.root, f.remover, nil)
	rule.SetSalvager(salvager)
	item := itemFor(t, f.runWith(rule, RunRequest{}), "open")
	if item.Status != StatusKept || !strings.Contains(item.Reason, "still open") || !exists(path) {
		t.Fatalf("item = %+v exists=%v, want kept with the open-PR reason", item, exists(path))
	}
	if exists(filepath.Join(salvager.Root(), "open")) {
		t.Fatal("salvaged a session with an open PR")
	}
}

func TestWorktreeRuleDryRunPlansSalvageWithoutWriting(t *testing.T) {
	f := newWorktreeFixture(t)
	path := dirtyWorktree(t, f, "plan")
	salvager := NewSalvager(t.TempDir(), nil)
	rule := NewWorktreeRule(f.root, f.remover, nil)
	rule.SetSalvager(salvager)
	item := itemFor(t, f.runWith(rule, RunRequest{DryRun: true}), "plan")
	if item.Status != StatusWouldFree || !exists(path) || exists(salvager.Root()) {
		t.Fatalf("dry run item = %+v; worktree exists=%v salvage written=%v", item, exists(path), exists(salvager.Root()))
	}
}

func TestSalvagingSafetyCheckSalvagesForManualCleanup(t *testing.T) {
	f := newWorktreeFixture(t)
	path := dirtyWorktree(t, f, "manual")
	salvager := NewSalvager(t.TempDir(), nil)
	rec := f.store.sessions[len(f.store.sessions)-1]
	if reason := SafetyCheck(f.root, nil)(context.Background(), rec, []string{path}); reason == "" {
		t.Fatal("SafetyCheck without a salvager no longer refuses a dirty worktree")
	}
	if reason := SalvagingSafetyCheck(f.root, nil, salvager)(context.Background(), rec, []string{path}); reason != "" {
		t.Fatalf("manual cleanup kept a salvageable worktree: %q", reason)
	}
	if !exists(filepath.Join(salvaged(t, salvager.Root(), "manual"), "changes.patch")) {
		t.Fatal("manual cleanup did not salvage")
	}
}

func TestPruneSalvageExpiresOldSalvage(t *testing.T) {
	data := t.TempDir()
	s := NewSalvager(data, nil)
	now := time.Now()
	old := filepath.Join(s.Root(), "s1", "old")
	fresh := filepath.Join(s.Root(), "s1", "fresh")
	for _, d := range []string{old, fresh} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(old, now.Add(-31*24*time.Hour), now.Add(-31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	run := &Run{Now: now, report: &CategoryReport{}}
	s.pruneSalvage(context.Background(), run)
	if exists(old) || !exists(fresh) {
		t.Fatalf("old exists=%v fresh exists=%v, want only the old salvage removed", exists(old), exists(fresh))
	}
}

// Untracked files past the total cap fail the salvage, and the worktree is
// kept rather than deleted with unsaved files.
func TestWorktreeRuleKeepsAWorktreeTooLargeToSalvage(t *testing.T) {
	old := salvageMaxTotal
	salvageMaxTotal = 4
	t.Cleanup(func() { salvageMaxTotal = old })
	f := newWorktreeFixture(t)
	path := dirtyWorktree(t, f, "big")
	rule := NewWorktreeRule(f.root, f.remover, nil)
	rule.SetSalvager(NewSalvager(t.TempDir(), nil))
	item := itemFor(t, f.runWith(rule, RunRequest{}), "big")
	if item.Status == StatusFreed || !exists(path) || !strings.Contains(item.Reason, "could not be salvaged") {
		t.Fatalf("item = %+v exists=%v, want kept because salvage failed", item, exists(path))
	}
}

func TestSalvageNoticeNamesADraftPR(t *testing.T) {
	s := NewSalvager(t.TempDir(), func(context.Context, domain.SessionID) ([]domain.PullRequest, error) {
		return []domain.PullRequest{{Draft: true}}, nil
	})
	why, ok := s.eligible(context.Background(), "w-1")
	if ok || !strings.Contains(why, "draft") {
		t.Fatalf("eligible = %q, %v; want a draft notice", why, ok)
	}
}
