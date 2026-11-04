package session

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type reportingStore struct {
	*fakeStore
	reports []domain.ReportRecord
}

func (s reportingStore) LatestNeedsInputReports(_ context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.ReportRecord, error) {
	out := map[domain.SessionID]domain.ReportRecord{}
	for _, r := range s.reports {
		for _, id := range ids {
			if r.SessionID == id {
				out[id] = r
			}
		}
	}
	return out, nil
}

func TestNeedsInputAudienceFromStoredFacts(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	worker := domain.SessionRecord{ID: "w1", ProjectID: "p", Kind: domain.KindWorker, Activity: domain.Activity{State: domain.ActivityIdle}}
	report := domain.ReportRecord{SessionID: "w1", State: domain.ReportNeedsInput, Note: "need a call", CreatedAt: now.Add(-time.Minute)}
	build := func(orchestrator bool, rep domain.ReportRecord) *Service {
		st := newFakeStore()
		if orchestrator {
			st.sessions["o1"] = domain.SessionRecord{ID: "o1", ProjectID: "p", Kind: domain.KindOrchestrator}
		}
		return &Service{store: reportingStore{fakeStore: st, reports: []domain.ReportRecord{rep}}}
	}
	ctx := context.Background()

	if got := build(true, report).NeedsInputAudience(ctx, worker, now); got != domain.NeedsInputOrchestrator {
		t.Fatalf("orchestrator-bound report: got %q", got)
	}
	if got := build(false, report).NeedsInputAudience(ctx, worker, now); got != domain.NeedsInputHuman {
		t.Fatalf("no live orchestrator: got %q", got)
	}
	acted := worker
	acted.Metadata.LatestInteractionAt = now
	if got := build(true, report).NeedsInputAudience(ctx, acted, now); got != domain.NeedsInputNone {
		t.Fatalf("orchestrator acted: got %q", got)
	}
	for _, state := range []domain.ActivityState{domain.ActivityWaitingInput, domain.ActivityBlocked} {
		paused := worker
		paused.Activity.State = state
		if got := build(true, report).NeedsInputAudience(ctx, paused, now); got != domain.NeedsInputHuman {
			t.Fatalf("agent-level wait %s after a report: got %q, want the human", state, got)
		}
	}
	if got := build(true, report).NeedsInputAudience(ctx, worker, now.Add(domain.OrchestratorWaitTimeout)); got != domain.NeedsInputHuman {
		t.Fatalf("timeout: got %q", got)
	}
	auto := report
	auto.Note = domain.SynthesizedReportPrefix + " input_required: blocked"
	if got := build(true, auto).NeedsInputAudience(ctx, worker, now); got != domain.NeedsInputHuman {
		t.Fatalf("agent-level wait: got %q", got)
	}
	orchestrator := domain.SessionRecord{ID: "o1", ProjectID: "p", Kind: domain.KindOrchestrator, Activity: domain.Activity{State: domain.ActivityWaitingInput}}
	if got := build(true, report).NeedsInputAudience(ctx, orchestrator, now); got != domain.NeedsInputNone {
		t.Fatalf("orchestrators are never worker waits: got %q", got)
	}
}

type interactionStore struct {
	Store
	stamped []domain.SessionID
}

func (s *interactionStore) RecordSessionInteraction(_ context.Context, id domain.SessionID, _ string, _ time.Time) error {
	s.stamped = append(s.stamped, id)
	return nil
}

func TestClaimPRRecordsAnInteractionThatEndsAnOrchestratorWait(t *testing.T) {
	claimer := &guardedFakePRClaimer{}
	svc := newGuardClaimFixture(t, claimer)
	store := &interactionStore{Store: svc.store}
	svc.store = store
	if _, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{}); err != nil {
		t.Fatal(err)
	}
	if len(store.stamped) != 0 {
		t.Fatalf("stamped %v, want nothing for a claim that is not direction from another caller", store.stamped)
	}
	if _, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{RecordInteraction: true}); err != nil {
		t.Fatal(err)
	}
	if len(store.stamped) != 1 || store.stamped[0] != "mer-1" {
		t.Fatalf("stamped %v, want the claimed session once", store.stamped)
	}
}
