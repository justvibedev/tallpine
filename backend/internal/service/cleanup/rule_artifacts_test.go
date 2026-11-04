package cleanup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/artifactversions"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/artifactstorage"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

type fakeArtifacts struct {
	mu       sync.Mutex
	dataDir  string
	pruned   []domain.SessionID
	retained []domain.SessionID
	action   artifactstorage.Action
	days     int
	// refuse makes the prune refuse, as for a session restored since the snapshot.
	refuse      bool
	protected   map[domain.SessionID]bool
	endedBefore time.Time
}

func (f *fakeArtifacts) Protected(context.Context) (map[domain.SessionID]bool, error) {
	return f.protected, nil
}

func (f *fakeArtifacts) PruneRegenerable(_ context.Context, id domain.SessionID, endedBefore time.Time) (int64, error) {
	f.mu.Lock()
	f.endedBefore = endedBefore
	if f.refuse {
		f.mu.Unlock()
		return 0, fmt.Errorf("%w: session is live", artifactversions.ErrPruneRefused)
	}
	f.pruned = append(f.pruned, id)
	f.mu.Unlock()
	dir := sessionartifacts.Dir(f.dataDir, id)
	_ = os.RemoveAll(filepath.Join(dir, sessionartifacts.VersionsDir))
	_ = os.RemoveAll(filepath.Join(dir, "node_modules"))
	return 200, nil
}

func (f *fakeArtifacts) Retain(_ context.Context, action artifactstorage.Action, days int, ids []domain.SessionID) ([]artifactstorage.Result, error) {
	f.mu.Lock()
	f.retained = append(f.retained, ids...)
	f.action, f.days = action, days
	f.mu.Unlock()
	return []artifactstorage.Result{{SessionID: ids[0], Status: artifactstorage.StatusDeleted, FreedBytes: 100}}, nil
}

type artifactFixture struct {
	t     *testing.T
	data  string
	now   time.Time
	store *fakeStore
	fake  *fakeArtifacts
}

func newArtifactFixture(t *testing.T, policy string) *artifactFixture {
	data := t.TempDir()
	return &artifactFixture{
		t: t, data: data, now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
		store: &fakeStore{policy: policy}, fake: &fakeArtifacts{dataDir: data},
	}
}

// session adds a session with a 100-byte deliverable, 100 bytes of version
// history and a 100-byte node_modules file.
func (f *artifactFixture) session(id domain.SessionID, terminated bool, endedAgo time.Duration) string {
	f.t.Helper()
	dir := sessionartifacts.Dir(f.data, id)
	for _, rel := range []string{"report.md", ".versions/blobs/ab/abc", "node_modules/x/index.js"} {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, 100), 0o600); err != nil {
			f.t.Fatal(err)
		}
	}
	f.store.sessions = append(f.store.sessions, domain.SessionRecord{ID: id, ProjectID: "mer", IsTerminated: terminated, UpdatedAt: f.now.Add(-endedAgo)})
	return dir
}

func (f *artifactFixture) run(req RunRequest) Report {
	f.t.Helper()
	rule := NewArtifactRule(f.data, f.fake, f.fake)
	svc := New(f.store, []Rule{rule}, Config{Pause: -1, StartDelay: -1, Clock: func() time.Time { return f.now }})
	report, err := svc.Run(context.Background(), req)
	if err != nil {
		f.t.Fatal(err)
	}
	return report
}

func itemsByWhat(report Report, id domain.SessionID) map[string]Item {
	out := map[string]Item{}
	for _, cat := range report.Categories {
		for _, item := range cat.Items {
			if item.SessionID == id {
				out[item.What] = item
			}
		}
	}
	return out
}

const month = 30 * 24 * time.Hour

func TestArtifactRuleLeavesLiveAndRecentSessionsAlone(t *testing.T) {
	f := newArtifactFixture(t, `{"artifacts":{"autoDeliverables":true}}`)
	live := f.session("live", false, 2*month)
	recent := f.session("recent", true, 24*time.Hour)
	report := f.run(RunRequest{ConfirmArtifacts: true})
	if len(f.fake.pruned) != 0 || len(f.fake.retained) != 0 {
		t.Fatalf("pruned=%v retained=%v, want nothing touched", f.fake.pruned, f.fake.retained)
	}
	if item := itemsByWhat(report, "recent")["artifacts"]; item.Status != StatusKept {
		t.Fatalf("recent session item = %+v, want kept", item)
	}
	if len(itemsByWhat(report, "live")) != 0 {
		t.Fatal("a live session's artifacts were reported as a candidate")
	}
	for _, dir := range []string{live, recent} {
		if !exists(filepath.Join(dir, "report.md")) || !exists(filepath.Join(dir, ".versions")) {
			t.Fatalf("%s changed", dir)
		}
	}
}

func TestArtifactRuleAsksBeforeHistoryAndDeliverables(t *testing.T) {
	f := newArtifactFixture(t, "{}")
	dir := f.session("old", true, 2*month)
	report := f.run(RunRequest{Trigger: TriggerSchedule})
	items := itemsByWhat(report, "old")
	if got := items["version history and tool directories"]; got.Status != StatusNeedsConfirmation || got.Bytes != 200 {
		t.Fatalf("history item = %+v, want 200 bytes pending confirmation", got)
	}
	if got := items["deliverables (archive)"]; got.Status != StatusNeedsConfirmation || got.Bytes != 100 {
		t.Fatalf("deliverables item = %+v, want 100 bytes pending confirmation", got)
	}
	if len(f.fake.pruned) != 0 || len(f.fake.retained) != 0 {
		t.Fatalf("pruned=%v retained=%v without confirmation", f.fake.pruned, f.fake.retained)
	}
	for _, kept := range []string{"report.md", ".versions", "node_modules"} {
		if !exists(filepath.Join(dir, kept)) {
			t.Fatalf("%s removed without confirmation", kept)
		}
	}
	if cat := report.Categories[0]; cat.PendingBytes != 300 || cat.FreedBytes != 0 {
		t.Fatalf("category totals = %+v", cat)
	}
}

func TestArtifactRuleConfirmationRemovesHistoryAndArchivesDeliverables(t *testing.T) {
	f := newArtifactFixture(t, "{}")
	f.session("old", true, 2*month)
	report := f.run(RunRequest{ConfirmArtifacts: true})
	items := itemsByWhat(report, "old")
	if got := items["version history and tool directories"]; got.Status != StatusFreed || got.Bytes != 200 {
		t.Fatalf("history item = %+v, want 200 bytes freed", got)
	}
	if len(f.fake.retained) != 1 || f.fake.action != artifactstorage.ActionArchive {
		t.Fatalf("retain calls=%v action=%s", f.fake.retained, f.fake.action)
	}
}

func TestArtifactRuleAutoPruneRemovesHistoryOnly(t *testing.T) {
	f := newArtifactFixture(t, `{"artifacts":{"autoPrune":true}}`)
	f.session("old", true, 2*month)
	report := f.run(RunRequest{Trigger: TriggerSchedule})
	items := itemsByWhat(report, "old")
	if got := items["version history and tool directories"]; got.Status != StatusFreed {
		t.Fatalf("history item = %+v, want freed", got)
	}
	if got := items["deliverables (archive)"]; got.Status != StatusNeedsConfirmation || len(f.fake.retained) != 0 {
		t.Fatalf("deliverables item = %+v retained=%v, want pending confirmation", got, f.fake.retained)
	}
}

func TestArtifactRuleReportsARefusedPruneAsKept(t *testing.T) {
	f := newArtifactFixture(t, `{"artifacts":{"autoPrune":true}}`)
	dir := f.session("old", true, 2*month)
	f.fake.refuse = true
	report := f.run(RunRequest{})
	item := itemsByWhat(report, "old")["version history and tool directories"]
	if item.Status != StatusKept || !strings.Contains(item.Reason, "live") || item.Bytes != 0 {
		t.Fatalf("refused prune item = %+v, want kept with the reason", item)
	}
	if !exists(filepath.Join(dir, ".versions")) {
		t.Fatal("history removed although the prune was refused")
	}
	if want := f.now.Add(-30 * 24 * time.Hour); !f.fake.endedBefore.Equal(want) {
		t.Fatalf("prune cutoff = %v, want now minus the retention %v", f.fake.endedBefore, want)
	}
}

func TestArtifactRuleAppliesDeliverableActionOnceConfirmed(t *testing.T) {
	f := newArtifactFixture(t, `{"artifacts":{"deliverableAction":"delete","retentionDays":14}}`)
	f.session("old", true, 2*month)
	report := f.run(RunRequest{ConfirmArtifacts: true})
	if len(f.fake.retained) != 1 || f.fake.action != artifactstorage.ActionDelete || f.fake.days != 14 {
		t.Fatalf("retain calls=%v action=%s days=%d", f.fake.retained, f.fake.action, f.fake.days)
	}
	if got := itemsByWhat(report, "old")["deliverables (delete)"]; got.Status != StatusFreed {
		t.Fatalf("deliverables item = %+v, want freed", got)
	}
}

func TestArtifactRuleAutoDeliverablesNeedsNoConfirmation(t *testing.T) {
	f := newArtifactFixture(t, `{"artifacts":{"autoDeliverables":true}}`)
	f.session("old", true, 2*month)
	f.run(RunRequest{Trigger: TriggerSchedule})
	if len(f.fake.retained) != 1 || f.fake.action != artifactstorage.ActionArchive {
		t.Fatalf("auto deliverables: retained=%v action=%s", f.fake.retained, f.fake.action)
	}
}

func TestArtifactRuleDryRunTouchesNothing(t *testing.T) {
	f := newArtifactFixture(t, "{}")
	dir := f.session("old", true, 2*month)
	report := f.run(RunRequest{DryRun: true, ConfirmArtifacts: true})
	if len(f.fake.pruned) != 0 || len(f.fake.retained) != 0 {
		t.Fatalf("dry run pruned=%v retained=%v", f.fake.pruned, f.fake.retained)
	}
	items := itemsByWhat(report, "old")
	if items["version history and tool directories"].Status != StatusWouldFree || items["deliverables (archive)"].Status != StatusWouldFree {
		t.Fatalf("dry run items = %+v", items)
	}
	if !exists(filepath.Join(dir, ".versions")) || !exists(filepath.Join(dir, "node_modules")) {
		t.Fatal("dry run removed files")
	}
}

func TestArtifactRuleSkipsCustomArtifactDirs(t *testing.T) {
	f := newArtifactFixture(t, `{"artifacts":{"autoDeliverables":true}}`)
	f.session("old", true, 2*month)
	f.store.sessions[0].Metadata.ArtifactDir = filepath.Join(t.TempDir(), "elsewhere")
	f.run(RunRequest{})
	if len(f.fake.pruned) != 0 || len(f.fake.retained) != 0 {
		t.Fatal("a session with a custom artifact dir was cleaned")
	}
}

func TestArtifactRuleKeepsSessionsALiveBacklogItemReferences(t *testing.T) {
	f := newArtifactFixture(t, `{"artifacts":{"autoPrune":true}}`)
	dir := f.session("old", true, 2*month)
	f.fake.protected = map[domain.SessionID]bool{"old": true}
	for _, req := range []RunRequest{{DryRun: true, ConfirmArtifacts: true}, {ConfirmArtifacts: true}} {
		report := f.run(req)
		items := itemsByWhat(report, "old")
		if len(items) != 1 || items["artifacts"].Status != StatusKept || items["artifacts"].Reason != artifactstorage.ProtectedReason {
			t.Fatalf("items = %+v, want one kept protected item", items)
		}
	}
	if len(f.fake.pruned) != 0 || len(f.fake.retained) != 0 || !exists(filepath.Join(dir, ".versions")) {
		t.Fatalf("pruned=%v retained=%v: protected session touched", f.fake.pruned, f.fake.retained)
	}
}
