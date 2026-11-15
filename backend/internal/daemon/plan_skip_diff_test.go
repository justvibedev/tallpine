package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/gitdefault"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// B737: the skipping diff leaves out the sibling's commit only.
func TestPlanBaseSkippingDiff(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(file string) string {
		if err := os.WriteFile(filepath.Join(repo, file), []byte(time.Now().String()), 0o600); err != nil {
			t.Fatal(err)
		}
		run("add", file)
		run("commit", "-m", file)
		return run("rev-parse", "HEAD")
	}
	run("init", "-b", "main")
	base := commit("base.txt")
	sib := commit("sibling.txt")
	commit("foreign.txt")
	run("remote", "add", "origin", repo)
	run("fetch", "origin")
	run("remote", "set-head", "origin", "main")

	store := sqlitetest.MustOpen(t)
	if err := store.UpsertProject(context.Background(), domain.ProjectRecord{ID: "p1", Path: repo, RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	b := planBase{store: store, resolver: gitdefault.New("", nil)}
	got, err := b.ChangedSinceSkipping(context.Background(), "p1", base, []string{sib})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "foreign.txt" {
		t.Fatalf("changed skipping sibling = %v, want [foreign.txt]", got)
	}
	lines, err := b.ChangedLinesSkipping(context.Background(), "p1", base, []string{sib}, []string{"foreign.txt", "sibling.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lines["sibling.txt"]; ok {
		t.Fatalf("sibling's file must not appear: %v", lines)
	}
}
