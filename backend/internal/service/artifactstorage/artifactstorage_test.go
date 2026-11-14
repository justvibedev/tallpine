package artifactstorage

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

type fakeSessions []domain.SessionRecord

func (f fakeSessions) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	return f, nil
}

// removeAll mimics Capturer.DeleteSession: files gone, empty directory left.
type removeAll struct {
	dataDir string
	err     error
	calls   []domain.SessionID
}

func (r *removeAll) DeleteSession(_ context.Context, id domain.SessionID) error {
	r.calls = append(r.calls, id)
	if r.err != nil {
		return r.err
	}
	dir := sessionartifacts.Dir(r.dataDir, id)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o700)
}

func put(t *testing.T, dataDir, id, rel, body string) {
	t.Helper()
	p := filepath.Join(sessionartifacts.Dir(dataDir, domain.SessionID(id)), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	dataDir string
	svc     *Service
	del     *removeAll
	now     time.Time
}

func newFixture(t *testing.T, warn int64) fixture {
	t.Helper()
	dataDir := t.TempDir()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	old, recent := now.AddDate(0, 0, -60), now.AddDate(0, 0, -2)
	put(t, dataDir, "old-done", "report.html", strings.Repeat("a", 100))
	put(t, dataDir, "old-done", ".versions/blobs/ab/abcdef", strings.Repeat("b", 400))
	put(t, dataDir, "old-done", "venv/lib/x.py", strings.Repeat("c", 50))
	put(t, dataDir, "recent-done", "notes.md", "hello")
	put(t, dataDir, "old-live", "plan.md", "plan")
	sessions := fakeSessions{
		{ID: "old-done", ProjectID: "p1", DisplayName: "Old", IsTerminated: true, UpdatedAt: old},
		{ID: "recent-done", ProjectID: "p1", IsTerminated: true, UpdatedAt: recent},
		{ID: "old-live", ProjectID: "p2", UpdatedAt: old},
		{ID: "empty", ProjectID: "p2", IsTerminated: true, UpdatedAt: old},
	}
	del := &removeAll{dataDir: dataDir}
	svc := New(dataDir, sessions, del, warn)
	svc.now = func() time.Time { return now }
	return fixture{dataDir: dataDir, svc: svc, del: del, now: now}
}

func TestReport_SizesWarningAndEligibility(t *testing.T) {
	f := newFixture(t, 500)
	r, err := f.svc.Report(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Entries) != 3 {
		t.Fatalf("entries = %d, want 3 (empty session omitted)", len(r.Entries))
	}
	top := r.Entries[0]
	if top.SessionID != "old-done" || top.SizeBytes != 550 || top.VersionsBytes != 400 || top.Files != 3 {
		t.Fatalf("top = %+v", top)
	}
	if !top.OverWarn || !top.Eligible {
		t.Fatalf("top should be over the 500-byte warning and eligible: %+v", top)
	}
	byID := map[domain.SessionID]Entry{}
	for _, e := range r.Entries {
		byID[e.SessionID] = e
	}
	if byID["recent-done"].Eligible || byID["old-live"].Eligible {
		t.Fatal("a recent or live session was marked eligible")
	}
	if r.EligibleBytes != 550 || r.TotalBytes != 550+5+4 || r.VersionsBytes != 400 {
		t.Fatalf("totals = %+v", r)
	}
}

func TestReport_RejectsBadWindow(t *testing.T) {
	f := newFixture(t, 0)
	if _, err := f.svc.Report(context.Background(), -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestRetain_ArchiveKeepsDeliverablesOnly(t *testing.T) {
	f := newFixture(t, 0)
	res, err := f.svc.Retain(context.Background(), ActionArchive, 30, []domain.SessionID{"old-done"})
	if err != nil || len(res) != 1 {
		t.Fatalf("res = %+v, err %v", res, err)
	}
	r := res[0]
	if r.Status != StatusArchived || r.FreedBytes != 550 || r.ArchivePath == "" {
		t.Fatalf("result = %+v", r)
	}
	if got := tarNames(t, r.ArchivePath); strings.Join(got, ",") != "report.html" {
		t.Fatalf("archive holds %v, want only report.html", got)
	}
	if entries, _ := os.ReadDir(sessionartifacts.Dir(f.dataDir, "old-done")); len(entries) != 0 {
		t.Fatalf("live artifact dir still has %d entries", len(entries))
	}
}

func TestRetain_DeleteMakesNoArchive(t *testing.T) {
	f := newFixture(t, 0)
	res, err := f.svc.Retain(context.Background(), ActionDelete, 30, []domain.SessionID{"old-done"})
	if err != nil || res[0].Status != StatusDeleted || res[0].ArchivePath != "" {
		t.Fatalf("res = %+v, err %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, ArchiveDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archive dir exists after delete: %v", err)
	}
}

func TestRetain_SkipsIneligibleSessions(t *testing.T) {
	f := newFixture(t, 0)
	res, err := f.svc.Retain(context.Background(), ActionDelete, 30, []domain.SessionID{"recent-done", "old-live", "ghost", "empty", "recent-done"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 4 {
		t.Fatalf("results = %d, want 4 (duplicate id collapsed)", len(res))
	}
	for _, r := range res {
		if r.Status != StatusSkipped {
			t.Errorf("%s: status %s, want skipped (%s)", r.SessionID, r.Status, r.Reason)
		}
	}
	if len(f.del.calls) != 0 {
		t.Fatalf("deleter called for %v", f.del.calls)
	}
	if _, err := os.Stat(filepath.Join(sessionartifacts.Dir(f.dataDir, "recent-done"), "notes.md")); err != nil {
		t.Fatalf("recent session's file was touched: %v", err)
	}
}

func TestRetain_SkipsSessionWithNonCanonicalArtifactDir(t *testing.T) {
	f := newFixture(t, 0)
	custom := t.TempDir()
	if err := os.WriteFile(filepath.Join(custom, "keep.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := domain.SessionRecord{ID: "elsewhere", IsTerminated: true, UpdatedAt: f.now.AddDate(0, 0, -60)}
	rec.Metadata.ArtifactDir = custom
	f.svc.sessions = fakeSessions{rec}

	r, err := f.svc.Report(context.Background(), 30)
	if err != nil || len(r.Entries) != 1 || r.Entries[0].Eligible || r.EligibleBytes != 0 {
		t.Fatalf("report = %+v, err %v", r, err)
	}
	res, err := f.svc.Retain(context.Background(), ActionDelete, 30, []domain.SessionID{"elsewhere"})
	if err != nil || res[0].Status != StatusSkipped || res[0].FreedBytes != 0 {
		t.Fatalf("res = %+v, err %v", res, err)
	}
	if len(f.del.calls) != 0 {
		t.Fatalf("deleter called for %v", f.del.calls)
	}
	if _, err := os.Stat(filepath.Join(custom, "keep.md")); err != nil {
		t.Fatalf("custom artifact dir was touched: %v", err)
	}
}

func TestRetain_FailedDeleteLeavesArchiveReportedFailed(t *testing.T) {
	f := newFixture(t, 0)
	f.del.err = errors.New("locked")
	res, err := f.svc.Retain(context.Background(), ActionArchive, 30, []domain.SessionID{"old-done"})
	if err != nil || res[0].Status != StatusFailed || res[0].FreedBytes != 0 {
		t.Fatalf("res = %+v, err %v", res, err)
	}
}

func TestRetain_RejectsBadRequests(t *testing.T) {
	f := newFixture(t, 0)
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"action": func() error { _, e := f.svc.Retain(ctx, "shred", 30, []domain.SessionID{"old-done"}); return e },
		"window": func() error { _, e := f.svc.Retain(ctx, ActionDelete, 0, []domain.SessionID{"old-done"}); return e },
		"empty":  func() error { _, e := f.svc.Retain(ctx, ActionDelete, 30, nil); return e },
	} {
		if err := call(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func tarNames(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
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

type fakeProtector struct {
	sessions, artifacts, texts []string
	err                        error
}

func (p fakeProtector) LiveBacklogArtifactRefs(context.Context) ([]string, []string, []string, error) {
	return p.sessions, p.artifacts, p.texts, p.err
}

func TestPathTokens(t *testing.T) {
	got := pathTokens("see agent-orchestrator-494/B202-design.md and (x_1/y) but not foo.md, nor a/ b my.app-3/d.md")
	want := []string{"agent-orchestrator-494", "x_1", "a", "my.app-3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("pathTokens = %v, want %v", got, want)
	}
}

func TestProtection_LiveBacklogReferencesKeepArtifacts(t *testing.T) {
	cases := map[string]fakeProtector{
		"linked session": {sessions: []string{"old-done"}},
		"note path":      {texts: []string{"design at ~/.tallpine/data/artifacts/old-done/design.md"}},
		"artifact link":  {artifacts: []string{"old-done/design.md"}},
		"bare artifact":  {artifacts: []string{"old-done"}},
	}
	for name, prot := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, 0)
			f.svc.SetProtector(prot)
			rep, err := f.svc.Report(context.Background(), 30)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range rep.Entries {
				if e.SessionID == "old-done" && (!e.Protected || e.Eligible) {
					t.Fatalf("old-done protected=%v eligible=%v", e.Protected, e.Eligible)
				}
			}
			if rep.EligibleBytes != 0 {
				t.Fatalf("EligibleBytes = %d, want 0", rep.EligibleBytes)
			}
			res, err := f.svc.Retain(context.Background(), ActionDelete, 30, []domain.SessionID{"old-done"})
			if err != nil {
				t.Fatal(err)
			}
			if res[0].Status != StatusSkipped || res[0].Reason != ProtectedReason || len(f.del.calls) != 0 {
				t.Fatalf("result %+v, deleter calls %v", res[0], f.del.calls)
			}
		})
	}
}

func TestProtection_PartialIDDoesNotMatch(t *testing.T) {
	f := newFixture(t, 0)
	f.svc.SetProtector(fakeProtector{texts: []string{"old-do/x.md and old-done-2/y.md"}})
	res, err := f.svc.Retain(context.Background(), ActionDelete, 30, []domain.SessionID{"old-done"})
	if err != nil || res[0].Status != StatusDeleted {
		t.Fatalf("res %+v err %v", res, err)
	}
}

func TestProtection_ReadFailureBlocksRetention(t *testing.T) {
	f := newFixture(t, 0)
	f.svc.SetProtector(fakeProtector{err: errors.New("db down")})
	if _, err := f.svc.Retain(context.Background(), ActionDelete, 30, []domain.SessionID{"old-done"}); err == nil {
		t.Fatal("Retain proceeded without knowing the references")
	}
	if len(f.del.calls) != 0 {
		t.Fatalf("deleter called: %v", f.del.calls)
	}
}

// A session that was live at the boot migration (stored dir = flat path, the
// canonical path a symlink to it) and ended afterwards is still retained.
func TestManagedAcceptsALinkedSessionsFlatDir(t *testing.T) {
	f := newFixture(t, 0)
	rec := domain.SessionRecord{ID: "linked-1", IsTerminated: true, UpdatedAt: f.now.AddDate(0, 0, -60)}
	rec.Metadata.ArtifactDir = sessionartifacts.LegacyDir(f.dataDir, rec.ID)
	if !f.svc.eligible(rec, f.now.AddDate(0, 0, -30)) {
		t.Fatal("a linked session's flat stored dir must count as managed")
	}
	rec.Metadata.ArtifactDir = "/elsewhere/artifacts/linked-1"
	if f.svc.eligible(rec, f.now.AddDate(0, 0, -30)) {
		t.Fatal("a custom dir must stay unmanaged")
	}
}

// A session created before the per-project layout stores its flat path and is
// still managed (retained, deletable); a custom dir is not.
func TestManagedAcceptsALegacyFlatDir(t *testing.T) {
	f := newFixture(t, 0)
	rec := domain.SessionRecord{ID: "legacy-1", IsTerminated: true, UpdatedAt: f.now.AddDate(0, 0, -60)}
	rec.Metadata.ArtifactDir = sessionartifacts.LegacyDir(f.dataDir, rec.ID)
	if !f.svc.eligible(rec, f.now.AddDate(0, 0, -30)) {
		t.Fatal("a legacy flat stored dir must count as managed")
	}
	rec.Metadata.ArtifactDir = "/elsewhere/artifacts/legacy-1"
	if f.svc.eligible(rec, f.now.AddDate(0, 0, -30)) {
		t.Fatal("a custom dir must stay unmanaged")
	}
}
