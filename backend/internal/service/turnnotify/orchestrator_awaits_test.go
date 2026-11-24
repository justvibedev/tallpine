package turnnotify

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// awaitsHarness is a chat worker's durable harness whose project has one
// orchestrator in the given activity state, with the production
// OrchestratorAwaits check bound.
func awaitsHarness(t *testing.T, orchestrator domain.ActivityState) *durableHarness {
	t.Helper()
	h := newDurableHarness(t, domain.SessionModeChat)
	_, err := h.st.CreateSession(context.Background(), domain.SessionRecord{
		ProjectID: h.rec.ProjectID, Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		CreatedAt: h.now, UpdatedAt: h.now, Activity: domain.Activity{State: orchestrator, LastActivityAt: h.now},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.o.OrchestratorAwaits = h.reports.OrchestratorAwaits
	return h
}

// turnEndedAvailableAfter completes the current turn, lets the fallback row
// become a report, and returns how long that report waits before delivery.
func (h *durableHarness) turnEndedAvailableAfter() time.Duration {
	h.t.Helper()
	h.complete("finished, nothing else to say")
	h.now = h.now.Add(time.Minute)
	h.o.deliverPending(context.Background())
	reports := h.autoReports()
	if len(reports) != 1 {
		h.t.Fatalf("turn_ended reports = %d, want 1", len(reports))
	}
	return reports[0].AvailableAt.Sub(h.now)
}

// Q23: an idle orchestrator waiting on the worker hears of its silent turn end
// at once instead of after ChatQuietDelay (phone_protector-49 ended 15:08:54,
// its orchestrator woke 15:10:54).
func TestChatTurnEndedImmediateWhenOrchestratorIdleAndAwaiting(t *testing.T) {
	h := awaitsHarness(t, domain.ActivityIdle)
	if wait := h.turnEndedAvailableAfter(); wait != 0 {
		t.Fatalf("available after %v, want at once", wait)
	}
}

// B192/B255 stay solved: a mid-turn orchestrator is not woken per worker turn,
// so the notice keeps its quiet window (and a worker report filed meanwhile
// still supersedes it).
func TestChatTurnEndedHeldWhileOrchestratorBusy(t *testing.T) {
	h := awaitsHarness(t, domain.ActivityActive)
	if wait := h.turnEndedAvailableAfter(); wait != ChatQuietDelay {
		t.Fatalf("available after %v, want %v", wait, ChatQuietDelay)
	}
}

// A worker that already reported done is not awaited: its later silent turn
// end keeps the hold even for an idle orchestrator.
func TestChatTurnEndedHeldWhenWorkerAlreadyDone(t *testing.T) {
	h := awaitsHarness(t, domain.ActivityIdle)
	h.report(domain.ReportDone)
	h.complete("turn one")
	h.now = h.now.Add(time.Minute)
	h.o.deliverPending(context.Background())
	h.start("turn-2")
	if wait := h.turnEndedAvailableAfter(); wait != ChatQuietDelay {
		t.Fatalf("available after %v, want %v", wait, ChatQuietDelay)
	}
}

// The live (non-durable) chat path applies the same policy.
func TestLiveChatTurnEndedFollowsOrchestratorAwaits(t *testing.T) {
	for _, awaits := range []bool{true, false} {
		h := newNudgeHarness()
		r := h.sessions["w1"]
		r.Mode, r.Metadata.ControllerGeneration = domain.SessionModeChat, "g1"
		h.sessions["w1"] = r
		var asked domain.SessionID
		h.o.OrchestratorAwaits = func(_ context.Context, rec domain.SessionRecord) bool {
			asked = rec.ID
			return awaits
		}
		_ = h.o.ApplyActivitySignal(context.Background(), "w1", ports.ActivitySignal{Event: "stop", ControllerGeneration: "g1", FinalAssistantMessage: "done"})
		h.fire()
		if len(h.reports.got) != 1 || asked != "w1" {
			t.Fatalf("awaits=%v: reports=%+v asked=%q, want one turn_ended and a check for w1", awaits, h.reports.got, asked)
		}
		got := h.reports.got[0]
		if awaits && (got.Quiet || got.QuietFor != 0) || !awaits && (!got.Quiet || got.QuietFor != ChatQuietDelay) {
			t.Fatalf("awaits=%v: quiet=%v quietFor=%v", awaits, got.Quiet, got.QuietFor)
		}
	}
}
