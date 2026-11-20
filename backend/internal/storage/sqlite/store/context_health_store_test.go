package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestContextHealthFindingRoundTripAndGuardedTransition(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p")
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	f := domain.ContextHealthFinding{
		ID: "f1", ProjectID: "p", Kind: domain.ContextInstructionBudget, Fingerprint: "instruction_budget",
		State: domain.ContextFindingOpen, Evidence: "ev", MeasuredTokens: 18000, BudgetTokens: 15000, AskID: "a1", AskRef: "Q1",
		CreatedAt: now, UpdatedAt: now,
	}
	mustNoError(t, s.UpsertFinding(ctx, f))
	// The same fingerprint rewrites the row and keeps its id and creation time.
	g := f
	g.ID, g.MeasuredTokens, g.CreatedAt = "other", 19000, now.Add(time.Hour)
	mustNoError(t, s.UpsertFinding(ctx, g))
	got, err := s.ListFindings(ctx, "p")
	mustNoError(t, err)
	if len(got) != 1 || got[0].ID != "f1" || got[0].MeasuredTokens != 19000 || !got[0].CreatedAt.Equal(now) || got[0].AskRef != "Q1" {
		t.Fatalf("findings = %+v", got)
	}
	byAsk, ok, err := s.FindingByAsk(ctx, "a1")
	if err != nil || !ok || byAsk.ID != "f1" {
		t.Fatalf("by ask = %+v %v %v", byAsk, ok, err)
	}
	// Only the first of two racing transitions from open wins.
	declined := got[0]
	declined.State, declined.DeclinedAt, declined.DeclinedTokens, declined.DeclineReason = domain.ContextFindingDeclined, now, 19000, "later"
	if ok, err := s.SaveFindingTransition(ctx, declined, domain.ContextFindingOpen); err != nil || !ok {
		t.Fatalf("first transition ok=%v err=%v", ok, err)
	}
	accepted := got[0]
	accepted.State = domain.ContextFindingAccepted
	if ok, err := s.SaveFindingTransition(ctx, accepted, domain.ContextFindingOpen); err != nil || ok {
		t.Fatalf("second transition ok=%v err=%v, want it refused", ok, err)
	}
	after, _ := s.ListFindings(ctx, "p")
	if after[0].State != domain.ContextFindingDeclined || after[0].DeclineReason != "later" || !after[0].DeclinedAt.Equal(now) {
		t.Fatalf("after = %+v", after[0])
	}
}

func TestContextHealthRunRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p")
	if _, ok, err := s.GetRun(ctx, "p"); err != nil || ok {
		t.Fatalf("fresh project has a run: ok=%v err=%v", ok, err)
	}
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	mustNoError(t, s.SaveRun(ctx, "p", at, "{}"))
	mustNoError(t, s.SaveRun(ctx, "p", at.Add(time.Hour), "{}"))
	if got, ok, err := s.GetRun(ctx, "p"); err != nil || !ok || !got.Equal(at.Add(time.Hour)) {
		t.Fatalf("run = %v %v %v", got, ok, err)
	}
}

func TestDaemonAskOriginRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p")
	sys := newAsk("a1", "p", "System?")
	sys.Lane, sys.LaneRef, sys.Origin = "", "", domain.AskOriginContextHealth
	if _, err := s.CreateOwnerAsk(ctx, sys, nil); err != nil {
		t.Fatal(err)
	}
	orch := newAsk("a2", "p", "Orchestrator?")
	if _, err := s.CreateOwnerAsk(ctx, orch, nil); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListOwnerAsks(ctx, "p")
	open, _ := s.ListOpenOwnerAsks(ctx, "p")
	one, ok, _ := s.GetOwnerAsk(ctx, "p", "Q1")
	if len(list) != 2 || list[0].Origin != domain.AskOriginContextHealth || list[1].Origin != domain.AskOriginOrchestrator ||
		len(open) != 2 || open[0].Origin != domain.AskOriginContextHealth || !ok || !one.FiledByDaemon() {
		t.Fatalf("origins wrong: list=%+v open=%+v one=%+v", list, open, one)
	}
}

func TestListFirstTurnInputsReadsTheFirstCallOfEachSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	sess := seedUsageSession(t, s, domain.HarnessCodex)
	binding := mustUpsertUsageBinding(t, s, sess, now, domain.UsageBindingRecord{NativeRootID: "root", State: domain.UsageBindingActive})
	source := mustInsertUsageSource(t, s, now, domain.UsageSourceRecord{
		BindingID: binding.ID, Kind: domain.UsageSourceCodexRollout, NativeSessionID: "t", ArtifactPath: "/tmp/r.jsonl",
		FileIdentity: "d:i", State: domain.UsageSourcePending,
	})
	mustNoError(t, s.ApplyUsageChunk(ctx, source.ID, 0, source.UpdatedAt, domain.SourceCursorState{ByteOffset: 10, State: domain.UsageSourceActive, UpdatedAt: now},
		[]domain.ModelUsageEvent{
			usageEvent("e1", canonicalUsageTokens(12000, 0, 12000, 5)),
			usageEvent("e2", canonicalUsageTokens(30000, 0, 30000, 5)),
		}))
	got, err := s.ListFirstTurnInputs(ctx, "usage", time.Now().Add(-24*time.Hour), 10)
	mustNoError(t, err)
	if len(got) != 1 || got[0].Tokens != 12000 || got[0].Harness != string(domain.HarnessCodex) {
		t.Fatalf("first-turn inputs = %+v, want the first call only", got)
	}
	if none, _ := s.ListFirstTurnInputs(ctx, "usage", time.Now().Add(48*time.Hour), 10); len(none) != 0 {
		t.Fatalf("sessions before the window were returned: %+v", none)
	}
}
