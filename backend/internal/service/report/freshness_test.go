package report

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestDelayedStatusSupersededWithoutLosingOutputsOrRequests(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	st := newCoordinatorStore(now)
	st.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	checkpoint := reportAt("checkpoint", "worker", now, now, "old remaining checks")
	checkpoint.State = domain.ReportCheckpoint
	checkpoint.Outputs = []domain.ReportOutput{{Kind: domain.ReportOutputArtifact, Reference: "build.zip"}}
	blocked := reportAt("blocked", "worker", now.Add(time.Second), now, "old blocked build")
	blocked.State = domain.ReportStuck
	request := reportAt("request", "worker", now.Add(2*time.Second), now, "choose a release destination")
	request.State = domain.ReportNeedsInput
	done := reportAt("done", "worker", now.Add(3*time.Second), now, "build succeeded")
	done.State = domain.ReportDone
	st.add(checkpoint, blocked, request, done)
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: st, Delivery: delivery, Now: func() time.Time { return now.Add(time.Minute) }})
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.interrupts) != 0 || len(delivery.submissions) != 1 {
		t.Fatalf("interrupts=%v submissions=%v", delivery.interrupts, delivery.submissions)
	}
	got := delivery.submissions[0].message
	for _, want := range []string{"build succeeded", "build.zip", "choose a release destination", "2026-10-26T12:00:03Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	for _, stale := range []string{"old remaining checks", "old blocked build"} {
		if strings.Contains(got, stale) {
			t.Errorf("stale status %q delivered: %s", stale, got)
		}
	}
	if st.acked != 4 {
		t.Fatalf("acked = %d", st.acked)
	}
}

func TestProgressSupersedesStuckButNotAnotherTaskOrInformation(t *testing.T) {
	now := time.Now().UTC()
	for _, direction := range []string{"", "human", "orchestrator"} {
		newTask := direction != ""
		st := newCoordinatorStore(now)
		worker := domain.SessionRecord{ID: "worker", ProjectID: "p", Kind: domain.KindWorker}
		switch direction {
		case "human":
			worker.Metadata.LatestUserPromptAt = now.Add(time.Second)
		case "orchestrator":
			worker.Metadata.LatestInteractionAt = now.Add(time.Second)
		}
		st.sessions = []domain.SessionRecord{worker}
		blocked := reportAt("blocked", worker.ID, now, now, "blocked")
		blocked.State = domain.ReportStuck
		info := reportAt("info", worker.ID, now, now, "different result")
		info.State, info.Message, info.Note = "", info.Note, ""
		progress := reportAt("progress", worker.ID, now.Add(2*time.Second), now, "progress")
		progress.State = domain.ReportCheckpoint
		c := NewCoordinator(CoordinatorDeps{Store: st})
		got, err := c.supersededStatuses(context.Background(), []domain.ReportRecord{blocked, info, progress})
		if err != nil {
			t.Fatal(err)
		}
		_, replaced := got[blocked.ID]
		if replaced == newTask {
			t.Fatalf("newTask=%v replaced=%v", newTask, replaced)
		}
		if _, replaced := got[info.ID]; replaced {
			t.Fatal("free-form information was superseded")
		}
	}
}

func TestRenewedBlockerSurvivesSupersededRepeat(t *testing.T) {
	now := time.Now().UTC()
	st := newCoordinatorStore(now)
	blocked := reportAt("blocked", "worker", now, now, "blocked again")
	blocked.State = domain.ReportStuck
	progress := reportAt("progress", "worker", now.Add(time.Second), now, "progress")
	progress.State = domain.ReportCheckpoint
	renewed := blocked
	renewed.ID, renewed.CreatedAt = "renewed", now.Add(2*time.Second)
	c := NewCoordinator(CoordinatorDeps{Store: st})
	batch := PreparedBatch{Reports: []domain.ReportRecord{blocked, progress, renewed}}
	var err error
	batch.Superseded, err = c.supersededStatuses(context.Background(), batch.Reports)
	if err != nil {
		t.Fatal(err)
	}
	got := batch.Render()
	if !strings.Contains(got, "blocked again") || !strings.Contains(got, renewed.CreatedAt.Format(time.RFC3339Nano)) || strings.Contains(got, "repeated 2 times") {
		t.Fatalf("renewed blocker was merged with obsolete status: %s", got)
	}
}
