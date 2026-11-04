package storageusage

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/cleanup"
	"tallpine.eu/tallpine/backend/internal/sessionreap"
)

type fakeSessions struct{ recs []domain.SessionRecord }

func (f fakeSessions) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	return f.recs, nil
}

func (f fakeSessions) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	for _, r := range f.recs {
		if r.ID == id {
			return r, true, nil
		}
	}
	return domain.SessionRecord{}, false, nil
}

type fakeCleaner struct{ reqs []cleanup.RunRequest }

func (f *fakeCleaner) Run(_ context.Context, req cleanup.RunRequest) (cleanup.Report, error) {
	f.reqs = append(f.reqs, req)
	return cleanup.Report{}, nil
}

func write(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	svc          *Service
	state, data  string
	cleaner      *fakeCleaner
	removedTrees []domain.SessionID
}

func newFixture(t *testing.T, recs ...domain.SessionRecord) *fixture {
	t.Helper()
	state := t.TempDir()
	data := filepath.Join(state, "data")
	f := &fixture{state: state, data: data, cleaner: &fakeCleaner{}}
	f.svc = New(Config{
		DataDir: data, StateRoot: state, GoCacheDir: filepath.Join(state, "gocache"),
		Sessions: fakeSessions{recs: recs}, Cleaner: f.cleaner,
		Worktrees: func(_ context.Context, id domain.SessionID) (string, error) {
			f.removedTrees = append(f.removedTrees, id)
			return "", nil
		},
		Processes: func() ([]sessionreap.Proc, error) { return nil, nil },
	})
	return f
}

func (f *fixture) entry(t *testing.T, snap Snapshot, rel string) Entry {
	t.Helper()
	for _, e := range snap.Entries {
		if e.Path == filepath.Join(f.state, rel) {
			return e
		}
	}
	t.Fatalf("no entry for %s in %+v", rel, snap.Entries)
	return Entry{}
}

func rec(id string, ended bool, kind domain.SessionKind) domain.SessionRecord {
	return domain.SessionRecord{ID: domain.SessionID(id), ProjectID: "proj", IsTerminated: ended, Kind: kind}
}

func TestSizesMatchDu(t *testing.T) {
	f := newFixture(t, rec("s1", true, domain.KindWorker))
	for i := range 40 {
		write(t, filepath.Join(f.data, "tmp", "s1", "d", strconv.Itoa(i)), 100_000+i)
	}
	if err := os.Link(filepath.Join(f.data, "tmp", "s1", "d", "0"), filepath.Join(f.data, "tmp", "s1", "linked")); err != nil {
		t.Fatal(err)
	}
	snap := f.svc.Refresh(context.Background())
	e := f.entry(t, snap, "data/tmp/s1")
	out, err := exec.Command("du", "-sk", e.Path).Output()
	if err != nil {
		t.Skip("no du")
	}
	kb, _ := strconv.ParseInt(strings.Fields(string(out))[0], 10, 64)
	want := kb * 1024
	if diff := e.Bytes - want; diff < -want/20 || diff > want/20 {
		t.Fatalf("size %d, du %d: more than 5%% apart", e.Bytes, want)
	}
	if e.Files != 40 || e.Session != "s1" || e.State != StateEnded || e.Newest.IsZero() {
		t.Fatalf("entry = %+v", e)
	}
}

func TestPartialWhenBudgetExpires(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a"), 10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if m := measureTree(ctx, dir); !m.Partial {
		t.Fatalf("cancelled walk not partial: %+v", m)
	}
}

func TestProtections(t *testing.T) {
	f := newFixture(t,
		rec("live", false, domain.KindWorker),
		rec("ended", true, domain.KindWorker),
		rec("orch", true, domain.KindOrchestrator),
	)
	for _, id := range []string{"live", "ended", "orch", "ghost"} {
		write(t, filepath.Join(f.data, "worktrees", "proj", id, "f"), 10)
		write(t, filepath.Join(f.data, "tmp", id, "f"), 10)
	}
	for _, rb := range []string{"20261001T000000Z-aa", "20261002T000000Z-bb", "20261003T000000Z-cc"} {
		write(t, filepath.Join(f.state, "releases", rb, "app", "x"), 10)
	}
	write(t, filepath.Join(f.state, "exports", "e.zip"), 10)
	ctx := context.Background()
	snap := f.svc.Refresh(ctx)

	cases := map[string]string{
		"data/worktrees/proj/live":     "live",
		"data/worktrees/proj/orch":     "orchestrator",
		"data/tmp/live":                "live",
		"releases/20261003T000000Z-cc": "newest",
	}
	for rel, want := range cases {
		if got := f.entry(t, snap, rel).Protected; !strings.Contains(got, want) {
			t.Errorf("%s protected = %q, want mention of %q", rel, got, want)
		}
	}
	for _, rel := range []string{"data/worktrees/proj/ended", "data/tmp/ended", "releases/20261001T000000Z-aa", "exports/e.zip"} {
		if got := f.entry(t, snap, rel).Protected; got != "" {
			t.Errorf("%s unexpectedly protected: %q", rel, got)
		}
	}
	if f.entry(t, snap, "data/worktrees/proj/ghost").State != StateOrphaned {
		t.Error("ghost worktree not orphaned")
	}

	for _, id := range []string{f.entry(t, snap, "data/worktrees/proj/live").ID, f.entry(t, snap, "data/tmp/live").ID,
		f.entry(t, snap, "releases/20261003T000000Z-cc").ID, f.entry(t, snap, "data/worktrees/proj/orch").ID,
		f.entry(t, snap, "data/worktrees/proj/ghost").ID} {
		var p ErrProtected
		if err := f.svc.Delete(ctx, id); !errors.As(err, &p) {
			t.Errorf("Delete(%s) = %v, want ErrProtected", id, err)
		}
	}
	if len(f.removedTrees) != 0 {
		t.Fatalf("worktree remover called for protected sessions: %v", f.removedTrees)
	}
	for _, rel := range []string{"data/tmp/live", "releases/20261003T000000Z-cc", "data/tmp/orch"} {
		if _, err := os.Stat(filepath.Join(f.state, rel)); err != nil {
			t.Errorf("%s was removed: %v", rel, err)
		}
	}

	// Removable entries go.
	if err := f.svc.Delete(ctx, f.entry(t, snap, "data/tmp/ended").ID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Delete(ctx, f.entry(t, snap, "exports/e.zip").ID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Delete(ctx, f.entry(t, snap, "data/worktrees/proj/ended").ID); err != nil || len(f.removedTrees) != 1 || f.removedTrees[0] != "ended" {
		t.Fatalf("worktree delete: err=%v removed=%v", err, f.removedTrees)
	}
	for _, rel := range []string{"data/tmp/ended", "exports/e.zip"} {
		if _, err := os.Stat(filepath.Join(f.state, rel)); !os.IsNotExist(err) {
			t.Errorf("%s still exists", rel)
		}
	}
	if err := f.svc.Delete(ctx, "exports:../../etc"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
}

func TestCleanSafeKeepsNewestRollbackAndUsesEngine(t *testing.T) {
	f := newFixture(t)
	for _, rb := range []string{"20261001T000000Z-aa", "20261002T000000Z-bb"} {
		write(t, filepath.Join(f.state, "releases", rb, "x"), 10)
	}
	// release-builds are named by commit: name order is the opposite of age.
	for name, age := range map[string]time.Duration{"zz": 3 * time.Hour, "aa": time.Hour, "mm": 0} {
		dir := filepath.Join(f.state, "release-builds", name)
		write(t, filepath.Join(dir, "x"), 10)
		at := time.Now().Add(-age)
		if err := os.Chtimes(dir, at, at); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := f.svc.CleanSafe(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.cleaner.reqs) != 1 || !f.cleaner.reqs[0].StorageSafe || f.cleaner.reqs[0].DryRun {
		t.Fatalf("engine requests = %+v", f.cleaner.reqs)
	}
	for _, cat := range f.cleaner.reqs[0].Only {
		if cat == cleanup.CategoryBranches || cat == cleanup.CategoryContainers || cat == cleanup.CategoryArtifacts {
			t.Errorf("clean-safe includes %s", cat)
		}
	}
	for dir, wantGone := range map[string]bool{
		"releases/20261001T000000Z-aa": true, "releases/20261002T000000Z-bb": false,
		"release-builds/zz": true, "release-builds/aa": true, "release-builds/mm": false,
	} {
		_, err := os.Stat(filepath.Join(f.state, dir))
		if gone := os.IsNotExist(err); gone != wantGone {
			t.Errorf("%s gone=%v want %v", dir, gone, wantGone)
		}
	}
	if rep.FreedBytes == 0 {
		t.Error("freed bytes not counted")
	}
	// A dry run removes nothing.
	write(t, filepath.Join(f.state, "releases", "20261001T000000Z-aa", "x"), 10)
	if _, err := f.svc.CleanSafe(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.state, "releases", "20261001T000000Z-aa")); err != nil {
		t.Error("dry run removed a rollback point")
	}
}

func TestProcessesListOnlyTerminatedKnownSessions(t *testing.T) {
	f := newFixture(t, rec("live", false, domain.KindWorker), rec("ended", true, domain.KindWorker))
	f.svc.cfg.Processes = func() ([]sessionreap.Proc, error) {
		return []sessionreap.Proc{
			{PID: 10, PPID: 1, Marker: "live", Exe: "/bin/live"},
			{PID: 11, PPID: 1, Marker: "ended", Exe: "/bin/vite", Start: time.Unix(1, 0)},
			{PID: 12, PPID: 1, Marker: "ghost", Exe: "/bin/ghost"},
			{PID: 13, PPID: 1, Exe: "/bin/unrelated"},
		}, nil
	}
	snap := f.svc.Refresh(context.Background())
	got := map[int]State{}
	for _, p := range snap.Processes {
		got[p.PID] = p.State
	}
	if len(got) != 1 || got[11] != StateEnded {
		t.Fatalf("processes = %+v", snap.Processes)
	}
}

func TestGoCacheCapReported(t *testing.T) {
	t.Setenv("TALLPINE_GOCACHE_MAX_GB", "7")
	f := newFixture(t)
	write(t, filepath.Join(f.state, "gocache", "ab", "x"), 4096)
	snap := f.svc.Refresh(context.Background())
	if snap.GoCache == nil || snap.GoCache.CapBytes != 7<<30 || snap.GoCache.Bytes < 4096 {
		t.Fatalf("go cache = %+v", snap.GoCache)
	}
}

func TestRecentReleaseBuildMayStillBeBuilding(t *testing.T) {
	f := newFixture(t)
	for name, age := range map[string]time.Duration{"old": 3 * time.Hour, "building": 2 * time.Minute, "newest": 0} {
		dir := filepath.Join(f.state, "release-builds", name)
		write(t, filepath.Join(dir, "x"), 10)
		at := time.Now().Add(-age)
		if err := os.Chtimes(dir, at, at); err != nil {
			t.Fatal(err)
		}
	}
	snap := f.svc.Refresh(context.Background())
	if got := f.entry(t, snap, "release-builds/building").Protected; !strings.Contains(got, "building") {
		t.Fatalf("recent build protected = %q", got)
	}
	if got := f.entry(t, snap, "release-builds/old").Protected; got != "" {
		t.Fatalf("old build protected = %q", got)
	}
}

func TestGradleCacheGoesThroughTheTrash(t *testing.T) {
	f := newFixture(t, rec("s1", true, domain.KindWorker))
	home := filepath.Join(f.data, "caches", "gradle", "proj")
	write(t, filepath.Join(home, "modules", "x"), 10)
	snap := f.svc.Refresh(context.Background())
	e := f.entry(t, snap, "data/caches/gradle/proj")
	if err := f.svc.Delete(context.Background(), e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("gradle home still in place")
	}
	// DiscardGradleHome renames into the cache trash first; in-place removal never creates it.
	if _, err := os.Stat(filepath.Join(f.data, "caches", ".trash")); err != nil {
		t.Fatalf("cache trash not used: %v", err)
	}
}

// removeTree keeps its strict meaning: a child of a known root may go, the
// root itself and a prefix sibling may not.
func TestRemoveTreeContainmentIsStrict(t *testing.T) {
	f := newFixture(t)
	child := filepath.Join(f.state, "child")
	write(t, filepath.Join(child, "f"), 1)
	if err := f.svc.removeTree(Entry{Path: child}); err != nil {
		t.Fatalf("child refused: %v", err)
	}
	if err := f.svc.removeTree(Entry{Path: f.state}); err == nil {
		t.Fatal("state root itself must be refused")
	}
	sibling := f.state + "-sibling"
	write(t, filepath.Join(sibling, "f"), 1)
	t.Cleanup(func() { _ = os.RemoveAll(sibling) })
	if err := f.svc.removeTree(Entry{Path: sibling}); err == nil {
		t.Fatal("prefix sibling must be refused")
	}
}
