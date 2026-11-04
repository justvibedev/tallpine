package localland

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/review"
)

type fakeStore struct {
	mu       sync.Mutex
	sessions map[domain.SessionID]domain.SessionRecord
	project  domain.ProjectRecord
	prs      map[domain.SessionID][]domain.PullRequest
	runs     map[domain.SessionID][]domain.ReviewRun
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.sessions[id]
	return r, ok, nil
}

func (f *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	return f.project, id == f.project.ID, nil
}

func (f *fakeStore) ListPRsBySession(_ context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prs[id], nil
}

func (f *fakeStore) ListReviewRunsBySession(_ context.Context, id domain.SessionID) ([]domain.ReviewRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs[id], nil
}

type mainResolver struct{}

func (mainResolver) ResolveDefaultBranch(context.Context, string, string) (ports.WorkspaceDefaultBranch, error) {
	return ports.WorkspaceDefaultBranch{Branch: "main"}, nil
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitFile(t *testing.T, dir, name, body, msg string) string {
	t.Helper()
	write(t, filepath.Join(dir, name), body)
	run(t, dir, "add", name)
	run(t, dir, "commit", "-q", "-m", msg)
	return run(t, dir, "rev-parse", "HEAD")
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	run(t, dir, "init", "-q", "-b", "main")
	run(t, dir, "config", "user.name", "Test")
	run(t, dir, "config", "user.email", "test@example.com")
	run(t, dir, "config", "commit.gpgsign", "false")
	commitFile(t, dir, "README.md", "hello\n", "init")
}

type fixture struct {
	repo  string
	store *fakeStore
	svc   *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, repo)
	store := &fakeStore{
		sessions: map[domain.SessionID]domain.SessionRecord{},
		project:  domain.ProjectRecord{ID: "p1", Path: repo},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		runs:     map[domain.SessionID][]domain.ReviewRun{},
	}
	return &fixture{repo: repo, store: store, svc: New(store, store, mainResolver{}, nil)}
}

// worker adds a session whose worktree branch carries one commit writing file.
func (f *fixture) worker(t *testing.T, id, file, body string) (domain.SessionID, string) {
	t.Helper()
	branch := "tp/" + id
	wt := filepath.Join(filepath.Dir(f.repo), id)
	run(t, f.repo, "worktree", "add", "-q", "-b", branch, wt, "main")
	head := commitFile(t, wt, file, body, "work on "+file)
	sid := domain.SessionID(id)
	f.store.mu.Lock()
	f.store.sessions[sid] = domain.SessionRecord{ID: sid, ProjectID: "p1", DisplayName: "Land " + id,
		Metadata: domain.SessionMetadata{Branch: branch, WorkspacePath: wt, DiffBaseRef: "main"}}
	f.store.mu.Unlock()
	return sid, head
}

func (f *fixture) approve(id domain.SessionID, sha string) {
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	branch := f.store.sessions[id].Metadata.Branch
	f.store.runs[id] = append(f.store.runs[id], domain.ReviewRun{ID: sha, SessionID: id, PRURL: review.LocalTargetURL(branch),
		TargetSHA: sha, Verdict: domain.VerdictApproved, CreatedAt: time.Now()})
}

func TestLandHappyPathSquashesAndFastForwardsCleanCheckout(t *testing.T) {
	f := newFixture(t)
	old := run(t, f.repo, "rev-parse", "main")
	id, _ := f.worker(t, "w1", "a.txt", "a\n")
	wt := f.store.sessions[id].Metadata.WorkspacePath
	head := commitFile(t, wt, "b.txt", "b\n", "second change")
	f.approve(id, head)

	res, err := f.svc.Land(context.Background(), id, Request{})
	if err != nil {
		t.Fatalf("Land: %v", err)
	}
	if res.Target != "main" || res.OldSHA != old || res.NewSHA == "" || res.Pushed {
		t.Fatalf("result = %+v", res)
	}
	if got := run(t, f.repo, "rev-parse", "main"); got != res.NewSHA {
		t.Fatalf("main = %s, want %s", got, res.NewSHA)
	}
	if parents := run(t, f.repo, "log", "-1", "--format=%P", "main"); parents != old {
		t.Fatalf("squash parents = %q, want only %s", parents, old)
	}
	msg := run(t, f.repo, "log", "-1", "--format=%B", "main")
	for _, want := range []string{"Land w1", "* work on a.txt", "* second change", "Tallpine-Session: w1", "Tallpine-Head: " + head} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message lacks %q:\n%s", want, msg)
		}
	}
	if len(res.UpdatedCheckouts) != 1 {
		t.Fatalf("updated checkouts = %v", res.UpdatedCheckouts)
	}
	if b, err := os.ReadFile(filepath.Join(f.repo, "b.txt")); err != nil || string(b) != "b\n" {
		t.Fatalf("checked-out main not fast-forwarded: %q %v", b, err)
	}
	if st := run(t, f.repo, "status", "--porcelain"); st != "" {
		t.Fatalf("checkout not clean after ff: %q", st)
	}
}

func TestLandRefusesDirtyCheckoutAndLeavesRef(t *testing.T) {
	f := newFixture(t)
	old := run(t, f.repo, "rev-parse", "main")
	id, head := f.worker(t, "w1", "a.txt", "a\n")
	f.approve(id, head)
	write(t, filepath.Join(f.repo, "README.md"), "edited\n")

	_, err := f.svc.Land(context.Background(), id, Request{})
	if !errors.Is(err, ErrDirtyCheckout) {
		t.Fatalf("err = %v, want ErrDirtyCheckout", err)
	}
	if got := run(t, f.repo, "rev-parse", "main"); got != old {
		t.Fatalf("main moved to %s", got)
	}
}

func TestLandRefusesUntrackedFileTheFastForwardWouldOverwrite(t *testing.T) {
	f := newFixture(t)
	old := run(t, f.repo, "rev-parse", "main")
	id, head := f.worker(t, "w1", "a.txt", "a\n")
	f.approve(id, head)
	write(t, filepath.Join(f.repo, "a.txt"), "mine\n")
	write(t, filepath.Join(f.repo, "other.txt"), "ignored\n")

	if _, err := f.svc.Land(context.Background(), id, Request{}); !errors.Is(err, ErrDirtyCheckout) {
		t.Fatalf("err = %v, want ErrDirtyCheckout", err)
	}
	if got := run(t, f.repo, "rev-parse", "main"); got != old {
		t.Fatalf("main moved to %s", got)
	}
}

func TestLandRefusesWithoutApprovalOfTheCurrentHead(t *testing.T) {
	f := newFixture(t)
	id, first := f.worker(t, "w1", "a.txt", "a\n")
	if _, err := f.svc.Land(context.Background(), id, Request{}); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("no approval: err = %v", err)
	}
	f.approve(id, first)
	commitFile(t, f.store.sessions[id].Metadata.WorkspacePath, "a.txt", "a2\n", "more")
	old := run(t, f.repo, "rev-parse", "main")
	_, err := f.svc.Land(context.Background(), id, Request{})
	if !errors.Is(err, ErrNotApproved) || !strings.Contains(err.Error(), "differs from approved head") {
		t.Fatalf("moved head: err = %v", err)
	}
	if got := run(t, f.repo, "rev-parse", "main"); got != old {
		t.Fatalf("main moved")
	}
}

func TestLandRefusesConflictWithFileList(t *testing.T) {
	f := newFixture(t)
	id, head := f.worker(t, "w1", "README.md", "branch\n")
	f.approve(id, head)
	moved := commitFile(t, f.repo, "README.md", "main\n", "main edit")

	_, err := f.svc.Land(context.Background(), id, Request{})
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("err = %v, want conflict naming README.md", err)
	}
	if got := run(t, f.repo, "rev-parse", "main"); got != moved {
		t.Fatalf("main moved")
	}
}

func TestLandCleanMergeOntoMovedTargetLands(t *testing.T) {
	f := newFixture(t)
	id, head := f.worker(t, "w1", "a.txt", "a\n")
	f.approve(id, head)
	moved := commitFile(t, f.repo, "c.txt", "c\n", "main moved")
	res, err := f.svc.Land(context.Background(), id, Request{})
	if err != nil || res.OldSHA != moved {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestLandTargetMovedBetweenReadAndUpdate(t *testing.T) {
	f := newFixture(t)
	id, head := f.worker(t, "w1", "a.txt", "a\n")
	f.approve(id, head)
	var moved string
	f.svc.beforeUpdate = func() { moved = commitFile(t, f.repo, "c.txt", "c\n", "racing commit") }

	_, err := f.svc.Land(context.Background(), id, Request{})
	if !errors.Is(err, ErrTargetMoved) {
		t.Fatalf("err = %v, want ErrTargetMoved", err)
	}
	if got := run(t, f.repo, "rev-parse", "main"); got != moved {
		t.Fatalf("main = %s, want the racing commit %s", got, moved)
	}
	if st := run(t, f.repo, "status", "--porcelain"); st != "" {
		t.Fatalf("checkout changed: %q", st)
	}
}

func TestLandIntoAnotherBranch(t *testing.T) {
	f := newFixture(t)
	oldMain := run(t, f.repo, "rev-parse", "main")
	run(t, f.repo, "branch", "feature")
	id, head := f.worker(t, "w1", "a.txt", "a\n")
	f.approve(id, head)

	res, err := f.svc.Land(context.Background(), id, Request{Into: "feature"})
	if err != nil || res.Target != "feature" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := run(t, f.repo, "rev-parse", "feature"); got != res.NewSHA {
		t.Fatalf("feature = %s", got)
	}
	if got := run(t, f.repo, "rev-parse", "main"); got != oldMain {
		t.Fatalf("main moved")
	}
	if _, err := f.svc.Land(context.Background(), id, Request{Into: "-x"}); !errors.Is(err, ErrBadTarget) {
		t.Fatalf("bad target err = %v", err)
	}
}

func TestReLandOfSameHeadIsNoOp(t *testing.T) {
	f := newFixture(t)
	id, head := f.worker(t, "w1", "a.txt", "a\n")
	f.approve(id, head)
	first, err := f.svc.Land(context.Background(), id, Request{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.svc.Land(context.Background(), id, Request{})
	if err != nil || !second.AlreadyLanded || second.NewSHA != first.NewSHA {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	if got := run(t, f.repo, "rev-list", "--count", "main"); got != "2" {
		t.Fatalf("main has %s commits, want 2", got)
	}
}

func TestLandRefusesSessionWithTrackedPR(t *testing.T) {
	f := newFixture(t)
	id, head := f.worker(t, "w1", "a.txt", "a\n")
	f.approve(id, head)
	f.store.prs[id] = []domain.PullRequest{{URL: "https://github.com/o/r/pull/7"}}
	_, err := f.svc.Land(context.Background(), id, Request{})
	if !errors.Is(err, ErrHasPR) || !strings.Contains(err.Error(), "pull/7") || !strings.Contains(err.Error(), "tallpine pr land") {
		t.Fatalf("err = %v", err)
	}
}

func TestLandDryRunMovesNothing(t *testing.T) {
	f := newFixture(t)
	old := run(t, f.repo, "rev-parse", "main")
	id, head := f.worker(t, "w1", "a.txt", "a\n")
	f.approve(id, head)
	res, err := f.svc.Land(context.Background(), id, Request{DryRun: true})
	if err != nil || !res.DryRun || res.NewSHA != "" || res.OldSHA != old {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := run(t, f.repo, "rev-parse", "main"); got != old {
		t.Fatalf("main moved")
	}
}

// remoteFixture: a bare origin, the project checkout cloned from it, and a
// project repo URL that names a hosted remote.
func remoteFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	bare := filepath.Join(filepath.Dir(f.repo), "origin.git")
	run(t, filepath.Dir(f.repo), "clone", "-q", "--bare", f.repo, bare)
	run(t, f.repo, "remote", "add", "origin", bare)
	run(t, f.repo, "fetch", "-q", "origin")
	f.store.project.RepoOriginURL = "https://github.com/o/r.git"
	return f, bare
}

func TestLandHostedRemotePushesAndFastForwardsLocal(t *testing.T) {
	f, bare := remoteFixture(t)
	id, head := f.worker(t, "w1", "a.txt", "a\n")
	f.approve(id, head)
	res, err := f.svc.Land(context.Background(), id, Request{})
	if err != nil || !res.Pushed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := run(t, bare, "rev-parse", "main"); got != res.NewSHA {
		t.Fatalf("origin main = %s, want %s", got, res.NewSHA)
	}
	if got := run(t, f.repo, "rev-parse", "main"); got != res.NewSHA {
		t.Fatalf("local main = %s, want %s", got, res.NewSHA)
	}
}

func TestLandHostedRemoteMovedAheadRejectsPush(t *testing.T) {
	f, bare := remoteFixture(t)
	other := filepath.Join(filepath.Dir(f.repo), "other")
	run(t, filepath.Dir(f.repo), "clone", "-q", bare, other)
	run(t, other, "config", "user.name", "Other")
	run(t, other, "config", "user.email", "o@example.com")
	oldLocal := run(t, f.repo, "rev-parse", "main")
	id, head := f.worker(t, "w1", "a.txt", "a\n")
	f.approve(id, head)
	var remote string
	f.svc.beforeUpdate = func() {
		remote = commitFile(t, other, "z.txt", "z\n", "remote moved")
		run(t, other, "push", "-q", "origin", "main")
	}
	_, err := f.svc.Land(context.Background(), id, Request{})
	if !errors.Is(err, ErrPushRejected) {
		t.Fatalf("err = %v, want ErrPushRejected", err)
	}
	if got := run(t, bare, "rev-parse", "main"); got != remote {
		t.Fatalf("origin main = %s, want %s", got, remote)
	}
	if got := run(t, f.repo, "rev-parse", "main"); got != oldLocal {
		t.Fatalf("local main moved to %s", got)
	}
}

func TestConcurrentLandsSerialize(t *testing.T) {
	f := newFixture(t)
	a, headA := f.worker(t, "wa", "a.txt", "a\n")
	b, headB := f.worker(t, "wb", "b.txt", "b\n")
	f.approve(a, headA)
	f.approve(b, headB)
	// Widen the read-to-update window so unserialized lands would collide.
	f.svc.beforeUpdate = func() { time.Sleep(300 * time.Millisecond) }

	var wg sync.WaitGroup
	results := make([]Result, 2)
	errs := make([]error, 2)
	for i, id := range []domain.SessionID{a, b} {
		wg.Add(1)
		go func(i int, id domain.SessionID) {
			defer wg.Done()
			results[i], errs[i] = f.svc.Land(context.Background(), id, Request{})
		}(i, id)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("land %d: %v", i, err)
		}
	}
	first, second := results[0], results[1]
	if second.OldSHA != first.NewSHA {
		first, second = second, first
	}
	if second.OldSHA != first.NewSHA {
		t.Fatalf("second land did not build on the first: %+v / %+v", results[0], results[1])
	}
	if got := run(t, f.repo, "rev-parse", "main"); got != second.NewSHA {
		t.Fatalf("main = %s, want %s", got, second.NewSHA)
	}
}
