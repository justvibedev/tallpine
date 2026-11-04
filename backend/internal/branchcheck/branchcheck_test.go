package branchcheck

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func git(t *testing.T, dir, date string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commits(t *testing.T, dir string, n int, date string) {
	t.Helper()
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%d-%s.txt", i, date[:4]))
		if err := os.WriteFile(p, []byte(date), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, dir, date, "add", "-A")
		git(t, dir, date, "commit", "-q", "-m", "c")
	}
}

// repo builds the legacy-master shape: master stopped in 2022, main continues from
// it. Returns the checkout (on branch `checkedOut`).
func repo(t *testing.T, mainCommits int, mainDate, checkedOut string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "2022-12-14T00:00:00Z", "init", "-q", "-b", "master")
	commits(t, dir, 1, "2022-12-14T00:00:00Z")
	git(t, dir, "2022-12-14T00:00:00Z", "branch", "main")
	git(t, dir, mainDate, "checkout", "-q", "main")
	commits(t, dir, mainCommits, mainDate)
	git(t, dir, mainDate, "checkout", "-q", checkedOut)
	return dir
}

func detect(t *testing.T, dir string) *Finding {
	t.Helper()
	f, err := Detect(context.Background(), dir, "origin", "refs/heads/master", MinNewer)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestAheadAndMuchNewerBranchIsReported(t *testing.T) {
	dir := repo(t, 22, "2026-10-01T00:00:00Z", "master")
	f := detect(t, dir)
	if f == nil || f.Branch != "main" || f.Ahead != 22 || f.NewerDays < 1000 || f.CheckedOut {
		t.Fatalf("finding = %+v", f)
	}
}

func TestCheckedOutBranchIsPreferredAndNeedNotBeWellKnown(t *testing.T) {
	dir := repo(t, 22, "2026-10-01T00:00:00Z", "main")
	git(t, dir, "2026-10-02T00:00:00Z", "checkout", "-q", "-b", "work")
	commits(t, dir, 1, "2026-10-02T00:00:00Z")
	f := detect(t, dir)
	if f == nil || f.Branch != "work" || !f.CheckedOut || f.Ahead != 23 {
		t.Fatalf("finding = %+v", f)
	}
}

func TestEqualBehindDivergedOrNotMuchNewerAreNotReported(t *testing.T) {
	// Equal: main points at the same commit as master.
	if f := detect(t, repo(t, 0, "2022-12-14T00:00:00Z", "master")); f != nil {
		t.Fatalf("equal: %+v", f)
	}
	// Ahead but only days newer.
	if f := detect(t, repo(t, 3, "2022-12-20T00:00:00Z", "master")); f != nil {
		t.Fatalf("not much newer: %+v", f)
	}
	// Behind: the default is the newer one; main is an old ancestor.
	dir := t.TempDir()
	git(t, dir, "2020-01-01T00:00:00Z", "init", "-q", "-b", "master")
	commits(t, dir, 1, "2020-01-01T00:00:00Z")
	git(t, dir, "2020-01-01T00:00:00Z", "branch", "main")
	commits(t, dir, 5, "2026-10-01T00:00:00Z")
	if f := detect(t, dir); f != nil {
		t.Fatalf("behind: %+v", f)
	}
	// Diverged: main has its own newer commits, master has its own too.
	dir = repo(t, 4, "2026-10-01T00:00:00Z", "master")
	commits(t, dir, 1, "2022-12-15T00:00:00Z")
	if f := detect(t, dir); f != nil {
		t.Fatalf("diverged: %+v", f)
	}
}

func TestRemoteTrackingBranchOnTheSameRemoteIsConsidered(t *testing.T) {
	dir := repo(t, 22, "2026-10-01T00:00:00Z", "master")
	git(t, dir, "2026-10-01T00:00:00Z", "update-ref", "refs/remotes/origin/main", "main")
	git(t, dir, "2026-10-01T00:00:00Z", "branch", "-D", "main")
	if f := detect(t, dir); f == nil || f.Branch != "main" {
		t.Fatalf("finding = %+v", f)
	}
}

func TestSameCommitIsNeverReportedEvenWithoutAnAgeThreshold(t *testing.T) {
	dir := repo(t, 0, "2022-12-14T00:00:00Z", "master")
	f, err := Detect(context.Background(), dir, "origin", "refs/heads/master", 0)
	if err != nil || f != nil {
		t.Fatalf("finding = %+v, err = %v", f, err)
	}
}

// Sessions start from origin/main once main is the default, so local commits that
// were never pushed do not make main a better default.
func TestUnpushedLocalCommitsDoNotCount(t *testing.T) {
	dir := repo(t, 22, "2026-10-01T00:00:00Z", "master")
	git(t, dir, "2022-12-14T00:00:00Z", "update-ref", "refs/remotes/origin/main", "master")
	if f := detect(t, dir); f != nil {
		t.Fatalf("finding = %+v", f)
	}
}

func TestCheckedOutBranchBeatsAFurtherAheadWellKnownOne(t *testing.T) {
	dir := repo(t, 22, "2026-10-01T00:00:00Z", "master")
	git(t, dir, "2026-10-02T00:00:00Z", "checkout", "-q", "-b", "work", "master")
	commits(t, dir, 1, "2026-10-02T00:00:00Z")
	f := detect(t, dir)
	if f == nil || f.Branch != "work" || !f.CheckedOut || f.Ahead != 1 {
		t.Fatalf("finding = %+v", f)
	}
}
