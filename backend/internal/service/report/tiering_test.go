package report_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// B192 report tiering, end to end on the real store: progress-only reports do
// not wake an idle orchestrator, actionable ones do, and a held report is
// never lost (it rides the next batch or the next chat turn, or is delivered
// once its quiet window ends).

type tierDelivery struct{ messages []string }

func (d *tierDelivery) Submit(_ context.Context, _ domain.SessionID, message, _ string) error {
	d.messages = append(d.messages, message)
	return nil
}
func (d *tierDelivery) Interrupt(context.Context, domain.SessionID) error { return nil }

type tierFixture struct {
	st       *sqlite.Store
	reports  *reportsvc.Service
	coord    *reportsvc.Coordinator
	delivery *tierDelivery
	now      *time.Time
}

// newTierFixture seeds project "w" with worker w-1 and chat orchestrator w-2.
func newTierFixture(t *testing.T) tierFixture {
	t.Helper()
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "w", Path: "/tmp/w", RegisteredAt: time.Now().UTC().Truncate(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "w", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode}); err != nil || rec.ID != "w-1" {
		t.Fatalf("seed worker: %q %v", rec.ID, err)
	}
	if rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "w", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat}); err != nil || rec.ID != "w-2" {
		t.Fatalf("seed orchestrator: %q %v", rec.ID, err)
	}
	if rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "w", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode}); err != nil || rec.ID != "w-3" {
		t.Fatalf("seed second worker: %q %v", rec.ID, err)
	}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	d := &tierDelivery{}
	return tierFixture{
		st: st, now: &now, delivery: d,
		reports: reportsvc.New(reportsvc.Deps{Store: st, Now: clock}),
		coord:   reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{Store: st, Delivery: d, Now: clock}),
	}
}

func (f tierFixture) file(t *testing.T, in reportsvc.CreateInput) {
	t.Helper()
	f.fileAs(t, "w-1", in)
}

func (f tierFixture) fileAs(t *testing.T, id domain.SessionID, in reportsvc.CreateInput) {
	t.Helper()
	in.SessionID = id
	if _, err := f.reports.Create(context.Background(), in); err != nil {
		t.Fatal(err)
	}
}

func (f tierFixture) run(t *testing.T) {
	t.Helper()
	if _, err := f.coord.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointDoesNotWakeButIsDeliveredAfterWindow(t *testing.T) {
	f := newTierFixture(t)
	f.file(t, reportsvc.CreateInput{State: domain.ReportCheckpoint, Note: "halfway"})
	f.run(t)
	if len(f.delivery.messages) != 0 {
		t.Fatalf("checkpoint woke the orchestrator: %q", f.delivery.messages)
	}
	*f.now = f.now.Add(reportsvc.DefaultQuietDelay + time.Second)
	f.run(t)
	if len(f.delivery.messages) != 1 || !strings.Contains(f.delivery.messages[0], "halfway") {
		t.Fatalf("checkpoint lost after the window: %q", f.delivery.messages)
	}
}

func TestQuietItemsCoalesceIntoOneTurnPerWindow(t *testing.T) {
	f := newTierFixture(t)
	f.file(t, reportsvc.CreateInput{State: domain.ReportCheckpoint, Note: "first"})
	*f.now = f.now.Add(4 * time.Minute)
	f.fileAs(t, "w-3", reportsvc.CreateInput{Message: "[auto] turn_ended: x", Synthesized: true, Quiet: true})
	*f.now = f.now.Add(reportsvc.DefaultQuietDelay - 4*time.Minute + time.Second)
	f.run(t)
	f.run(t)
	if len(f.delivery.messages) != 1 || !strings.Contains(f.delivery.messages[0], "first") || !strings.Contains(f.delivery.messages[0], "turn_ended") {
		t.Fatalf("want one turn carrying both quiet items: %q", f.delivery.messages)
	}
}

func TestActionableWakesAtOnceAndCarriesHeldItems(t *testing.T) {
	for _, state := range []domain.ReportState{domain.ReportNeedsInput, domain.ReportStuck, domain.ReportDone} {
		t.Run(string(state), func(t *testing.T) {
			f := newTierFixture(t)
			f.file(t, reportsvc.CreateInput{State: domain.ReportCheckpoint, Note: "held progress"})
			f.fileAs(t, "w-3", reportsvc.CreateInput{State: state, Note: "act now"})
			f.run(t)
			if len(f.delivery.messages) != 1 || !strings.Contains(f.delivery.messages[0], "act now") || !strings.Contains(f.delivery.messages[0], "held progress") {
				t.Fatalf("want one immediate delivery carrying both: %q", f.delivery.messages)
			}
		})
	}
}

func TestCheckpointWithOutputWakesAtOnce(t *testing.T) {
	for _, kind := range []domain.ReportOutputKind{domain.ReportOutputArtifact, domain.ReportOutputPRCreated} {
		t.Run(string(kind), func(t *testing.T) {
			f := newTierFixture(t)
			ref := "https://github.com/o/r/pull/1"
			if kind == domain.ReportOutputArtifact {
				ref = "result.md"
			}
			f.file(t, reportsvc.CreateInput{State: domain.ReportCheckpoint, Note: "output", Outputs: []domain.ReportOutput{{Kind: kind, Reference: ref}}})
			f.run(t)
			if len(f.delivery.messages) != 1 {
				t.Fatalf("checkpoint with %s held: %q", kind, f.delivery.messages)
			}
		})
	}
}

func TestHumanMessageCarriesHeldCheckpoint(t *testing.T) {
	ctx := context.Background()
	f := newTierFixture(t)
	f.file(t, reportsvc.CreateInput{State: domain.ReportCheckpoint, Note: "held progress"})
	batch, err := f.coord.PreparePiggyback(ctx, "w-2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(batch.AppendToUserMessage("hi"), "held progress") {
		t.Fatalf("held checkpoint missing from the human's turn: %+v", batch.Reports)
	}
	if err := f.coord.AcceptPiggyback(ctx, batch); err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(reportsvc.DefaultQuietDelay + time.Second)
	f.run(t)
	if len(f.delivery.messages) != 0 {
		t.Fatalf("piggybacked checkpoint delivered twice: %q", f.delivery.messages)
	}
}

func TestQuietTurnEndedSupersededBySameSessionReportIsDiscarded(t *testing.T) {
	f := newTierFixture(t)
	f.file(t, reportsvc.CreateInput{Message: "[auto] turn_ended: stale", Synthesized: true, Quiet: true})
	*f.now = f.now.Add(time.Minute)
	f.file(t, reportsvc.CreateInput{State: domain.ReportDone, Note: "all done"})
	f.run(t)
	*f.now = f.now.Add(reportsvc.DefaultQuietDelay + time.Second)
	f.run(t)
	joined := strings.Join(f.delivery.messages, "\n")
	if !strings.Contains(joined, "all done") || strings.Contains(joined, "stale") {
		t.Fatalf("want only the newer report: %q", f.delivery.messages)
	}
}

func TestQuietTurnEndedOfTerminatedSenderIsDiscarded(t *testing.T) {
	f := newTierFixture(t)
	f.file(t, reportsvc.CreateInput{Message: "[auto] turn_ended: gone", Synthesized: true, Quiet: true})
	rec, _, err := f.st.GetSession(context.Background(), "w-1")
	if err != nil {
		t.Fatal(err)
	}
	rec.IsTerminated = true
	if err := f.st.UpdateSession(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(reportsvc.DefaultQuietDelay + time.Second)
	f.run(t)
	if len(f.delivery.messages) != 0 {
		t.Fatalf("terminated sender's quiet report delivered: %q", f.delivery.messages)
	}
}

func TestQuietRestartNoticeSurvivesCarrierLaterReportAndTermination(t *testing.T) {
	f := newTierFixture(t)
	f.file(t, reportsvc.CreateInput{Message: "[auto] restart: app restarted: resumed 2 workers.", Synthesized: true, Quiet: true})
	*f.now = f.now.Add(time.Minute)
	f.file(t, reportsvc.CreateInput{State: domain.ReportCheckpoint, Note: "later"})
	rec, _, err := f.st.GetSession(context.Background(), "w-1")
	if err != nil {
		t.Fatal(err)
	}
	rec.IsTerminated = true
	if err := f.st.UpdateSession(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(reportsvc.DefaultQuietDelay + time.Second)
	f.run(t)
	if joined := strings.Join(f.delivery.messages, "\n"); !strings.Contains(joined, "[auto] restart") {
		t.Fatalf("restart notice lost: %q", f.delivery.messages)
	}
}
