package cleanup

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

var scratchNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func runRules(t *testing.T, store Store, req RunRequest, rules ...Rule) Report {
	t.Helper()
	svc := New(store, rules, Config{Pause: -1, StartDelay: -1, Clock: func() time.Time { return scratchNow }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	report, err := svc.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func writeFile(t *testing.T, path string, size int, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Dir(path), mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

// age sets every entry under root to mtime.
func age(t *testing.T, root string, mtime time.Time) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, mtime, mtime)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func terminated(id domain.SessionID, project domain.ProjectID, endedAgo time.Duration) domain.SessionRecord {
	return domain.SessionRecord{ID: id, ProjectID: project, IsTerminated: true, UpdatedAt: scratchNow.Add(-endedAgo)}
}

func live(id domain.SessionID, project domain.ProjectID) domain.SessionRecord {
	return domain.SessionRecord{ID: id, ProjectID: project, UpdatedAt: scratchNow}
}

// restoringStore reports a session as live when it is re-read, as if it was
// restored after the run's snapshot.
type restoringStore struct {
	*fakeStore
	mu        sync.Mutex
	reads     int
	restored  domain.SessionID
	liveAfter int
}

func (s *restoringStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok, err := s.fakeStore.GetSession(ctx, id)
	if id == s.restored {
		rec.IsTerminated = false
	}
	return rec, ok, err
}

func (s *restoringStore) ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	recs, err := s.fakeStore.ListAllSessions(ctx)
	s.mu.Lock()
	s.reads++
	reads := s.reads
	s.mu.Unlock()
	if reads > s.liveAfter {
		recs = append(recs, live("newcomer", "mer"))
	}
	return recs, err
}

func TestSessionTempRuleRemovesOnlyEndedSessionsLeftovers(t *testing.T) {
	data := t.TempDir()
	tmp := func(id string) string { return filepath.Join(data, sessionscratch.TempRoot, id) }
	old := scratchNow.Add(-48 * time.Hour)
	for _, id := range []string{"live-1", "ended-1", "ended-now", "unknown-old", "unknown-new", "restored-1"} {
		writeFile(t, filepath.Join(tmp(id), "f.bin"), 100, old)
	}
	writeFile(t, filepath.Join(tmp("unknown-new"), "f.bin"), 100, scratchNow.Add(-time.Minute))
	writeFile(t, filepath.Join(data, sessionscratch.TempRoot, "stray-file"), 10, old)
	writeFile(t, filepath.Join(data, sessionscratch.TempRoot, sessionscratch.TrashDir, "x-1", "f.bin"), 50, old)
	store := &restoringStore{fakeStore: &fakeStore{policy: "{}", sessions: []domain.SessionRecord{
		live("live-1", "mer"), terminated("ended-1", "mer", 2*time.Hour), terminated("ended-now", "mer", time.Minute),
		terminated("restored-1", "mer", 2*time.Hour),
	}}, restored: "restored-1", liveAfter: 1 << 30}
	rule := NewSessionTempRule(data)

	dry := runRules(t, store, RunRequest{DryRun: true}, rule)
	if got := dry.Categories[0].WouldFreeBytes; got != 100+100+100+50 {
		t.Fatalf("dry run would free %d, want ended-1 + unknown-old + restored-1 + trash", got)
	}
	for _, id := range []string{"ended-1", "unknown-old"} {
		if !exists(tmp(id)) {
			t.Fatalf("dry run removed %s", id)
		}
	}

	report := runRules(t, store, RunRequest{}, rule)
	if report.FreedBytes != 250 {
		t.Fatalf("freed %d, want 250 (ended-1, unknown-old, trash)", report.FreedBytes)
	}
	for _, id := range []string{"ended-1", "unknown-old"} {
		if exists(tmp(id)) {
			t.Errorf("%s still present", id)
		}
	}
	for _, id := range []string{"live-1", "ended-now", "unknown-new", "restored-1", "stray-file"} {
		if !exists(tmp(id)) {
			t.Errorf("%s removed; it belongs to a live, just-ended, new or restored session (or is not a session dir)", id)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(data, sessionscratch.TempRoot, sessionscratch.TrashDir)); len(entries) != 0 {
		t.Fatalf("trash not emptied: %v", entries)
	}
}

func TestHarnessCacheRuleKeepsCachesOfActiveProjects(t *testing.T) {
	data := t.TempDir()
	home := func(name string) string {
		return filepath.Join(data, sessionscratch.CacheRoot, sessionscratch.GradleDir, name)
	}
	for _, name := range []string{"busy", "recent", "idle", "orphan", sessionscratch.StandaloneGradleHome} {
		writeFile(t, filepath.Join(home(name), "caches", "modules-2", "dep.jar"), 1000, scratchNow.Add(-90*24*time.Hour))
	}
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "gradle.properties"), 5, time.Time{})
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(outside, "gradle.properties"), filepath.Join(home("idle"), "gradle.properties")); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(home("orphan"), scratchNow.Add(-90*24*time.Hour), scratchNow.Add(-90*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{policy: "{}", sessions: []domain.SessionRecord{
		live("b-1", "busy"), terminated("b-0", "busy", 60*24*time.Hour),
		terminated("r-1", "recent", 2*24*time.Hour),
		terminated("i-1", "idle", 30*24*time.Hour),
		live("s-1", ""),
	}}
	rule := NewHarnessCacheRule(data)

	if dry := runRules(t, store, RunRequest{DryRun: true}, rule); dry.Categories[0].WouldFreeBytes != 2000 {
		t.Fatalf("dry run would free %d, want idle + orphan", dry.Categories[0].WouldFreeBytes)
	}
	if !exists(home("idle")) {
		t.Fatal("dry run removed a cache")
	}
	report := runRules(t, store, RunRequest{}, rule)
	if report.FreedBytes != 2000 || exists(home("idle")) || exists(home("orphan")) {
		t.Fatalf("freed %d; idle=%t orphan=%t", report.FreedBytes, exists(home("idle")), exists(home("orphan")))
	}
	for _, name := range []string{"busy", "recent", sessionscratch.StandaloneGradleHome} {
		if !exists(home(name)) {
			t.Errorf("%s cache removed", name)
		}
	}
	if !exists(filepath.Join(outside, "gradle.properties")) {
		t.Fatal("the user's gradle.properties behind the link was removed")
	}
}

func TestHarnessCacheRuleRechecksForANewLiveSession(t *testing.T) {
	data := t.TempDir()
	home := filepath.Join(data, sessionscratch.CacheRoot, sessionscratch.GradleDir, "mer")
	writeFile(t, filepath.Join(home, "caches", "dep.jar"), 1000, time.Time{})
	store := &restoringStore{fakeStore: &fakeStore{policy: "{}", sessions: []domain.SessionRecord{terminated("m-1", "mer", 30*24*time.Hour)}}, liveAfter: 1}
	report := runRules(t, store, RunRequest{}, NewHarnessCacheRule(data))
	if report.FreedBytes != 0 || !exists(home) {
		t.Fatal("removed a cache a session started using after the snapshot")
	}
}

// A session that starts while an idle cache is being removed creates a fresh
// home, and that home survives the removal.
func TestHarnessCacheRuleNeverRemovesAHomeRecreatedMeanwhile(t *testing.T) {
	data := t.TempDir()
	home := filepath.Join(data, sessionscratch.CacheRoot, sessionscratch.GradleDir, "mer")
	writeFile(t, filepath.Join(home, "caches", "old.jar"), 1000, time.Time{})
	store := &fakeStore{policy: "{}", sessions: []domain.SessionRecord{terminated("m-1", "mer", 30*24*time.Hour)}}
	rule := NewHarnessCacheRule(data)
	rule.afterDiscard = func() {
		if _, err := sessionscratch.EnsureGradleHome(data, "mer", ""); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(home, "caches", "new.jar"), 10, time.Time{})
	}
	report := runRules(t, store, RunRequest{}, rule)
	if report.FreedBytes != 1000 {
		t.Fatalf("freed %d, want the old cache", report.FreedBytes)
	}
	if !exists(filepath.Join(home, "caches", "new.jar")) || exists(filepath.Join(home, "caches", "old.jar")) {
		t.Fatal("the recreated home was not left alone, or the old cache survived")
	}
	trash := filepath.Join(data, sessionscratch.CacheRoot, sessionscratch.CacheTrashDir)
	if entries, _ := os.ReadDir(trash); len(entries) != 0 {
		t.Fatalf("cache trash not emptied: %v", entries)
	}
}

// A cache whose removal a previous pass could not finish is emptied by the
// next one.
func TestHarnessCacheRuleEmptiesTheCacheTrash(t *testing.T) {
	data := t.TempDir()
	trash := filepath.Join(data, sessionscratch.CacheRoot, sessionscratch.CacheTrashDir)
	writeFile(t, filepath.Join(trash, "gradle-mer-1", "caches", "dep.jar"), 500, time.Time{})
	writeFile(t, filepath.Join(data, sessionscratch.CacheRoot, sessionscratch.GradleDir, "app", "dep.jar"), 10, time.Time{})
	store := &fakeStore{policy: "{}", sessions: []domain.SessionRecord{live("a-1", "app")}}
	if dry := runRules(t, store, RunRequest{DryRun: true}, NewHarnessCacheRule(data)); dry.Categories[0].WouldFreeBytes != 500 || !exists(filepath.Join(trash, "gradle-mer-1")) {
		t.Fatalf("dry run: would free %d, want the trash reported and kept", dry.Categories[0].WouldFreeBytes)
	}
	if report := runRules(t, store, RunRequest{}, NewHarnessCacheRule(data)); report.FreedBytes != 500 || exists(filepath.Join(trash, "gradle-mer-1")) {
		t.Fatalf("freed %d, want the trash emptied", report.FreedBytes)
	}
}

func TestSystemTempRuleOnlyRemovesWhenTurnedOn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ownership is not read on Windows")
	}
	tmp := t.TempDir()
	data := filepath.Join(tmp, "tmp.vo-data")
	old := scratchNow.Add(-10 * 24 * time.Hour)
	writeFile(t, filepath.Join(tmp, "release-check.abc", "app", "big.bin"), 4000, old)
	writeFile(t, filepath.Join(tmp, "release-check.busy", "old.bin"), 100, old)
	writeFile(t, filepath.Join(tmp, "release-check.busy", "deep", "new.bin"), 100, scratchNow.Add(-time.Hour))
	writeFile(t, filepath.Join(tmp, "release-check.new", "f.bin"), 100, scratchNow.Add(-time.Hour))
	writeFile(t, filepath.Join(tmp, "com.apple.thing", "f.bin"), 100, old)
	writeFile(t, filepath.Join(data, "ao.db"), 100, old)
	target := t.TempDir()
	writeFile(t, filepath.Join(target, "keep.bin"), 100, old)
	if err := os.Symlink(target, filepath.Join(tmp, "release-check.link")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"release-check.abc", "com.apple.thing", "tmp.vo-data"} {
		age(t, filepath.Join(tmp, name), old)
	}
	rule := NewSystemTempRule(tmp, data)
	rule.inUse = func(context.Context, string) (bool, error) { return false, nil }
	patterns := `"patterns":["release-check.*","tmp.*"]`

	off := runRules(t, &fakeStore{policy: `{"systemTemp":{` + patterns + `}}`}, RunRequest{}, rule)
	if cat := off.Categories[0]; cat.PendingBytes != 4000 || cat.FreedBytes != 0 || cat.Pending != 1 {
		t.Fatalf("while off: %+v, want release-check.abc reported only", cat)
	}
	if !exists(filepath.Join(tmp, "release-check.abc")) {
		t.Fatal("removed while the rule is off")
	}

	on := runRules(t, &fakeStore{policy: `{"systemTemp":{"enabled":true,` + patterns + `}}`}, RunRequest{}, rule)
	if on.FreedBytes != 4000 || exists(filepath.Join(tmp, "release-check.abc")) {
		t.Fatalf("freed %d, want release-check.abc removed", on.FreedBytes)
	}
	for _, kept := range []string{"release-check.busy", "release-check.new", "com.apple.thing", "release-check.link", filepath.Join("tmp.vo-data", "ao.db")} {
		if _, err := os.Lstat(filepath.Join(tmp, kept)); err != nil {
			t.Errorf("%s removed: %v", kept, err)
		}
	}
	if !exists(filepath.Join(target, "keep.bin")) {
		t.Fatal("symlink target removed")
	}
}

func TestSystemTempPatternValidation(t *testing.T) {
	for _, bad := range []string{`{"systemTemp":{"patterns":["*"]}}`, `{"systemTemp":{"patterns":["a/b*"]}}`, `{"systemTemp":{"patterns":["[x"]}}`, `{"systemTemp":{"minAgeDays":0}}`} {
		if _, err := ApplyPatch(DefaultPolicy(), []byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	next, err := ApplyPatch(DefaultPolicy(), []byte(`{"systemTemp":{"patterns":["gradle-build-*"]}}`))
	if err != nil || len(next.SystemTemp.Patterns) != 1 || next.SystemTemp.MinAgeDays != 3 {
		t.Fatalf("patterns replace = %+v, %v", next.SystemTemp, err)
	}
}

// By default the rule only matches names VO itself would create: tmp.* is
// every program's mktemp -d name, tallpine-* the official Agent Orchestrator's.
func TestSystemTempRuleDefaultsOnlyMatchVONames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ownership is not read on Windows")
	}
	tmp := t.TempDir()
	old := scratchNow.Add(-10 * 24 * time.Hour)
	for _, name := range []string{"tmp.abc123", "tallpine-launch-1", "release-check.x", "vo-old"} {
		writeFile(t, filepath.Join(tmp, name, "f.bin"), 100, old)
		age(t, filepath.Join(tmp, name), old)
	}
	rule := NewSystemTempRule(tmp, filepath.Join(tmp, "data"))
	rule.inUse = func(context.Context, string) (bool, error) { return false, nil }
	report := runRules(t, &fakeStore{policy: `{"systemTemp":{"enabled":true}}`}, RunRequest{}, rule)
	if report.FreedBytes != 100 || exists(filepath.Join(tmp, "vo-old")) {
		t.Fatalf("freed %d, want only vo-old", report.FreedBytes)
	}
	for _, name := range []string{"tmp.abc123", "tallpine-launch-1", "release-check.x"} {
		if !exists(filepath.Join(tmp, name)) {
			t.Errorf("%s removed by the default patterns", name)
		}
	}
}

// Any part of a leftover the rule cannot read keeps all of it: its age and
// what a removal would take are not known.
func TestSystemTempRuleKeepsALeftoverItCannotReadCompletely(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix permissions and a non-root user")
	}
	tmp := t.TempDir()
	old := scratchNow.Add(-10 * 24 * time.Hour)
	leftover := filepath.Join(tmp, "vo-old")
	writeFile(t, filepath.Join(leftover, "readable.bin"), 100, old)
	writeFile(t, filepath.Join(leftover, "locked", "hidden.bin"), 100, scratchNow)
	age(t, leftover, old)
	if err := os.Chmod(filepath.Join(leftover, "locked"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(leftover, "locked"), 0o700) })
	rule := NewSystemTempRule(tmp, filepath.Join(tmp, "data"))
	rule.inUse = func(context.Context, string) (bool, error) { return false, nil }
	report := runRules(t, &fakeStore{policy: `{"systemTemp":{"enabled":true}}`}, RunRequest{}, rule)
	if report.FreedBytes != 0 || !exists(filepath.Join(leftover, "readable.bin")) {
		t.Fatalf("freed %d; a partly unreadable leftover was removed", report.FreedBytes)
	}
	if cat := report.Categories[0]; len(cat.Items) != 1 || !strings.Contains(cat.Items[0].Reason, "could not be read") {
		t.Fatalf("items = %+v, want it kept with the reason", cat.Items)
	}
}

// A leftover a process still uses, or one the probe cannot judge, is kept.
func TestSystemTempRuleKeepsALeftoverInUse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ownership is not read on Windows")
	}
	tmp := t.TempDir()
	old := scratchNow.Add(-10 * 24 * time.Hour)
	for _, name := range []string{"vo-busy", "vo-unknown"} {
		writeFile(t, filepath.Join(tmp, name, "f.bin"), 100, old)
		age(t, filepath.Join(tmp, name), old)
	}
	rule := NewSystemTempRule(tmp, filepath.Join(tmp, "data"))
	rule.inUse = func(_ context.Context, path string) (bool, error) {
		if filepath.Base(path) == "vo-busy" {
			return true, nil
		}
		return false, errors.New("lsof: not found")
	}
	report := runRules(t, &fakeStore{policy: `{"systemTemp":{"enabled":true}}`}, RunRequest{}, rule)
	if report.FreedBytes != 0 || !exists(filepath.Join(tmp, "vo-busy")) || !exists(filepath.Join(tmp, "vo-unknown")) {
		t.Fatalf("freed %d; a busy or unjudged leftover was removed", report.FreedBytes)
	}
}

// The real probe sees a file this process holds open.
func TestPathInUseSeesAnOpenFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no in-use probe on Windows")
	}
	if runtime.GOOS != "linux" {
		if _, err := exec.LookPath("lsof"); err != nil {
			t.Skip("lsof not installed")
		}
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "deep", "held.bin"), 10, time.Time{})
	if busy, err := pathInUse(context.Background(), dir); err != nil || busy {
		t.Fatalf("idle dir: busy=%t err=%v", busy, err)
	}
	f, err := os.Open(filepath.Join(dir, "deep", "held.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if busy, err := pathInUse(context.Background(), dir); err != nil || !busy {
		t.Fatalf("held file: busy=%t err=%v", busy, err)
	}
}
