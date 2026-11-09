package restartresume

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
)

// capturingReports rejects a repeated id, as the store's primary key does.
type capturingReports struct {
	inputs []reportsvc.CreateInput
	seen   map[string]bool
}

func (c *capturingReports) Create(_ context.Context, in reportsvc.CreateInput) (domain.ReportRecord, error) {
	if c.seen[in.ID] {
		return domain.ReportRecord{}, domain.ErrDuplicateReport
	}
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	c.seen[in.ID] = true
	c.inputs = append(c.inputs, in)
	return domain.ReportRecord{}, nil
}

// B192 still holds: a restart where every worker resumed stays quiet.
func TestNotifyRestartStaysQuietWhenEveryWorkerResumed(t *testing.T) {
	ctx := context.Background()
	tr, _ := newTracker(t, worker("w-1", domain.ActivityIdle, false))
	if err := tr.save([]Entry{{SessionID: "w-1", InterruptedAt: time.Now().Add(-time.Minute).UTC()}}); err != nil {
		t.Fatal(err)
	}
	reports := &capturingReports{}
	tr.Resume(ctx, &fakeSender{})
	tr.NotifyRestart(ctx, reports)
	if len(reports.inputs) != 1 || !reports.inputs[0].Quiet {
		t.Fatalf("reports = %+v, want one quiet notice", reports.inputs)
	}
}

// B622: a worker cut off by a force-quit that never came back must not vanish
// silently: its orchestrator hears about it, loudly, once Resume gives up.
func TestNotifyRestartTellsOrchestratorAboutWorkerLeftStopped(t *testing.T) {
	ctx := context.Background()
	stopped := worker("w-stopped", domain.ActivityExited, false)
	gone := worker("w-gone", domain.ActivityExited, true)
	tr, _ := newTracker(t, stopped, gone)
	tr.attempts = 2
	at := time.Now().Add(-time.Minute).UTC()
	if err := tr.save([]Entry{{SessionID: "w-stopped", InterruptedAt: at}, {SessionID: "w-gone", InterruptedAt: at}}); err != nil {
		t.Fatal(err)
	}

	sender, reports := &fakeSender{}, &capturingReports{}
	tr.Resume(ctx, sender)
	tr.NotifyRestart(ctx, reports)

	if len(sender.sent) != 0 {
		t.Fatalf("sent = %v, want no resume message to a stopped worker", sender.sent)
	}
	if len(reports.inputs) != 1 {
		t.Fatalf("reports = %+v, want one notice", reports.inputs)
	}
	got := reports.inputs[0]
	if !strings.Contains(got.Message, "did not resume 1 workers: w-stopped") || strings.Contains(got.Message, "w-gone") {
		t.Fatalf("message = %q, want it to name only the stopped worker", got.Message)
	}
	if got.Quiet {
		t.Fatal("a worker left stopped must wake the orchestrator, not wait as a quiet report")
	}
}

// B622 review: entries left stopped stay on file, so a later boot reports them
// again. A worker newly stopped on that boot must still be delivered, and an
// unchanged set must not repeat.
func TestNotResumedNoticeDeliversNewWorkerOnLaterBootAndDedupesSameSet(t *testing.T) {
	ctx := context.Background()
	tr, _ := newTracker(t, worker("w-a", domain.ActivityExited, false), worker("w-b", domain.ActivityExited, false))
	tr.attempts = 1
	at := time.Now().Add(-time.Minute).UTC()
	a, b := Entry{SessionID: "w-a", InterruptedAt: at}, Entry{SessionID: "w-b", InterruptedAt: at.Add(time.Second)}
	reports := &capturingReports{}
	boot := func(entries ...Entry) {
		t.Helper()
		if err := tr.save(entries); err != nil {
			t.Fatal(err)
		}
		tr.Resume(ctx, &fakeSender{})
		tr.NotifyRestart(ctx, reports)
	}

	boot(a)
	boot(a, b)
	if len(reports.inputs) != 2 || !strings.Contains(reports.inputs[1].Message, "w-a, w-b") {
		t.Fatalf("reports = %+v, want the second boot's notice to name both workers", reports.inputs)
	}
	boot(a, b)
	if len(reports.inputs) != 2 {
		t.Fatalf("reports = %+v, want an unchanged stopped set not to repeat", reports.inputs)
	}
}

// B622 review: a worker restored after its notice and cut off again within
// maxAge is a new interruption, even though its old entry is still on file.
func TestNotResumedNoticeDeliveredForSecondCutOffOfSameWorker(t *testing.T) {
	ctx := context.Background()
	tr, st := newTracker(t, worker("w-a", domain.ActivityExited, false))
	tr.attempts = 1
	t1 := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	if err := tr.save([]Entry{{SessionID: "w-a", InterruptedAt: t1}}); err != nil {
		t.Fatal(err)
	}
	reports := &capturingReports{}
	tr.Resume(ctx, &fakeSender{})
	tr.NotifyRestart(ctx, reports)

	t2 := t1.Add(time.Hour)
	tr.saveMaybe(map[domain.SessionID]time.Time{"w-a": t2})
	if err := tr.RecordUnclean(ctx, nil); err != nil {
		t.Fatal(err)
	}
	st.recs["w-a"] = worker("w-a", domain.ActivityExited, false)
	tr.Resume(ctx, &fakeSender{})
	tr.NotifyRestart(ctx, reports)

	if len(reports.inputs) != 2 {
		t.Fatalf("reports = %+v, want a second notice for the new cut-off", reports.inputs)
	}
	if !strings.Contains(reports.inputs[1].Message, t2.Format(time.RFC3339)) {
		t.Fatalf("message = %q, want the new cut-off time", reports.inputs[1].Message)
	}
}

func TestRecordRefreshesKnownEntryOnlyWithNewerActivity(t *testing.T) {
	ctx := context.Background()
	cutOff := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	for name, tc := range map[string]struct {
		lastActivity time.Time
		wantRefresh  bool
	}{
		"working again since the cut-off": {cutOff.Add(time.Minute), true},
		"stale active state":              {cutOff.Add(-time.Minute), false},
	} {
		t.Run(name, func(t *testing.T) {
			rec := worker("w-a", domain.ActivityActive, false)
			rec.Activity.LastActivityAt = tc.lastActivity
			tr, _ := newTracker(t, rec)
			if err := tr.save([]Entry{{SessionID: "w-a", InterruptedAt: cutOff}}); err != nil {
				t.Fatal(err)
			}
			if err := tr.Record(ctx); err != nil {
				t.Fatal(err)
			}
			got := tr.load()
			if len(got) != 1 || got[0].InterruptedAt.After(cutOff) != tc.wantRefresh {
				t.Fatalf("entries = %+v, want refreshed=%v from %v", got, tc.wantRefresh, cutOff)
			}
		})
	}
}

// B627: a host lost just before a Force Quit leaves the worker Exited, which
// recordMaybe cannot see. NoteCutOff must get it recorded at the next boot, and
// must not displace the turns the shutdown itself noted.
func TestNoteCutOffIsRecordedAtNextBootAlongsideNotedTurns(t *testing.T) {
	ctx := context.Background()
	tr, _ := newTracker(t,
		worker("w-exited", domain.ActivityExited, false),
		worker("w-active", domain.ActivityActive, false),
		worker("w-bystander", domain.ActivityExited, false),
	)
	if err := tr.RecordIfQuit(ctx, false); err != nil {
		t.Fatal(err)
	}
	cut := time.Now().Add(-5 * time.Second).UTC()
	tr.NoteCutOff("w-exited", cut)
	if maybe := tr.loadMaybe(); len(maybe) != 2 || maybe["w-active"].IsZero() {
		t.Fatalf("maybe = %v, want the noted turn kept beside the cut-off", maybe)
	}
	if err := tr.RecordUnclean(ctx, nil); err != nil {
		t.Fatal(err)
	}
	got := map[domain.SessionID]time.Time{}
	for _, e := range tr.load() {
		got[e.SessionID] = e.InterruptedAt
	}
	if len(got) != 2 {
		t.Fatalf("entries = %v, want w-exited and w-active only", got)
	}
	if at := got["w-exited"]; !at.Equal(cut) {
		t.Fatalf("w-exited interrupted at %v, want %v", at, cut)
	}
	if _, ok := got["w-active"]; !ok {
		t.Fatalf("entries = %v, lost the turn the shutdown noted", got)
	}
}
