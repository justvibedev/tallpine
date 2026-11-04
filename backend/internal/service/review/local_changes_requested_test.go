package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitIn(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "add "+name)
}

// seedLocalChangesRequested seeds a worker with no PR, an unpushed branch with
// one commit, and a changes-requested run on that head. It returns the repo.
func seedLocalChangesRequested(t *testing.T, st *sqlite.Store, at time.Time, body string) string {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	commitIn(t, dir, "a.txt")
	gitIn(t, dir, "checkout", "-q", "-b", "feat")
	commitIn(t, dir, "f.txt")
	head := gitIn(t, dir, "rev-parse", "feat")
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "w", Path: "/tmp/w", RegisteredAt: time.Now().UTC().Truncate(time.Second)}); err != nil {
		t.Fatal(err)
	}
	rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "w", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{WorkspacePath: dir, Branch: "feat", DiffBaseRef: "main"}})
	if err != nil || rec.ID != "w-1" {
		t.Fatalf("seed worker: %v %q", err, rec.ID)
	}
	if got, _, _ := st.GetSession(ctx, rec.ID); got.Metadata.Branch != "feat" || got.Metadata.DiffBaseRef != "main" {
		t.Fatalf("metadata not persisted: %+v", got.Metadata)
	}
	if err := st.UpsertReview(ctx, domain.Review{ID: "rev-w-1", SessionID: "w-1", ProjectID: "w", Harness: domain.ReviewerCodex, CreatedAt: time.Unix(0, 0).UTC(), UpdatedAt: time.Unix(0, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertReviewRun(ctx, domain.ReviewRun{ID: "run-local", ReviewID: "rev-w-1", SessionID: "w-1", Harness: domain.ReviewerCodex,
		PRURL: "local:feat", TargetSHA: head, Status: domain.ReviewRunRunning, CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.UpdateReviewRunResult(ctx, "run-local", domain.ReviewRunComplete, domain.VerdictChangesRequested, body, "", true); err != nil || !ok {
		t.Fatalf("complete run: ok=%v err=%v", ok, err)
	}
	return dir
}

func TestLocalChangesRequestedNudgesWorkerWithBody(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	t0 := time.Unix(1000, 0).UTC()
	body := "Needs changes.\nmain.go:12 - missing nil check"
	seedLocalChangesRequested(t, st, t0, body)
	nudger := &fakeWorkerNudger{}
	svc := New(nil, st, WithClock(func() time.Time { return t0.Add(time.Minute) }))
	svc.SetFailureNotice(st, &fakeFailureReports{}, nudger)
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(nudger.messages) != 1 || !strings.Contains(nudger.messages[0], "main.go:12 - missing nil check") || !strings.Contains(nudger.messages[0], "do not push") {
		t.Fatalf("nudges = %q", nudger.messages)
	}
}

func TestLocalChangesRequestedMovedHeadClaimsWithoutNudge(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	t0 := time.Unix(1000, 0).UTC()
	dir := seedLocalChangesRequested(t, st, t0, "main.go:1 - x")
	commitIn(t, dir, "g.txt") // the worker moved on before the sweep
	nudger := &fakeWorkerNudger{}
	svc := New(nil, st, WithClock(func() time.Time { return t0.Add(time.Minute) }))
	svc.SetFailureNotice(st, &fakeFailureReports{}, nudger)
	if err := svc.NotifyEndedRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(nudger.messages) != 0 {
		t.Fatalf("nudges = %q, want none for an old head", nudger.messages)
	}
	if left, err := st.ListUnnotifiedEndedReviewRuns(ctx, 10); err != nil || len(left) != 0 {
		t.Fatalf("unnotified = %+v err=%v, want the run claimed", left, err)
	}
}
