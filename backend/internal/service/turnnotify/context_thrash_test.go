package turnnotify

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// thrashStop is Claude Code's stop text when its auto-compaction breaker trips.
const thrashStop = "Autocompact is thrashing: the context refilled to the limit within 3 turns of the previous compact, 3 times in a row. " +
	"A file being read or a tool output is likely too large for the context window. Try reading in smaller chunks, or use /clear to start fresh."

func assertContextThrashNotice(t *testing.T, msg string) {
	t.Helper()
	if !strings.HasPrefix(msg, "[auto] context_thrash: ") || !strings.Contains(msg, "respawn the task on a model with a larger context window") ||
		!strings.Contains(msg, "Autocompact is thrashing") || strings.Contains(msg, "went idle") ||
		strings.Contains(msg, "Picking up") || strings.Contains(msg, lastMessageLabel) {
		t.Fatalf("message = %q", msg)
	}
}

// B732: a chat turn the agent stopped on the auto-compaction thrash reaches the
// orchestrator as its own actionable notice, not as an idle worker quoting the
// sentence it wrote before the stop.
func TestChatTurnStoppedByContextThrashReportsRespawn(t *testing.T) {
	h := chatHarness()
	h.chat("chat.turn.started", "g1", "", domain.ActivityActive)
	_ = h.o.ApplyActivitySignal(context.Background(), "w1", ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "chat.turn.completed", ControllerGeneration: "g1",
		FinalAssistantMessage: "Picking up at the HubApp mapping step.", ContextThrash: thrashStop,
	})
	h.fire()
	if len(h.reports.got) != 1 {
		t.Fatalf("reports = %d, want 1", len(h.reports.got))
	}
	got := h.reports.got[0]
	assertContextThrashNotice(t, got.Message)
	if got.Quiet {
		t.Fatal("a thrash stop is actionable and must not wait in the quiet queue")
	}
}

// B732: the durable fallback row (the path chat workers use in production)
// renders the same notice and wakes the orchestrator.
func TestContextThrashFallbackReportsRespawn(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeChat)
	h.complete(domain.ContextThrashFallbackExcerpt(thrashStop))
	h.now = h.now.Add(time.Minute)
	h.o.deliverPending(context.Background())
	reports := h.autoReports()
	if len(reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(reports))
	}
	assertContextThrashNotice(t, reports[0].Message)
	if quiet, _, err := h.st.QuietReportState(context.Background(), reports[0].ID); err != nil || quiet {
		t.Fatal("a thrash stop is actionable and must not wait in the quiet queue")
	}
}

// A worker closed while its thrash stop is still pending gets the same notice.
func TestUndeliveredTurnResultKeepsContextThrashKind(t *testing.T) {
	h := newHarness(domain.KindWorker)
	h.o.Fallbacks = &closeFallbacks{pending: []domain.TurnFallback{
		{SessionID: "w1", Excerpt: domain.ContextThrashFallbackExcerpt(thrashStop)},
	}}
	assertContextThrashNotice(t, h.undelivered())
}

// A plain turn end is unchanged.
func TestTurnEndKindLeavesPlainTextAlone(t *testing.T) {
	if kind, detail := turnEndKind("Autocompact is thrashing, I quoted it"); kind != KindTurnEnded || detail != "Autocompact is thrashing, I quoted it" {
		t.Fatalf("kind=%q detail=%q", kind, detail)
	}
}

// B557 window: lifecycle closes the worker inside the thrash turn's own signal.
func TestUndeliveredTurnResultDuringClosingThrashSignal(t *testing.T) {
	h := chatHarness()
	var during string
	h.o.Next = applierFunc(func(context.Context, domain.SessionID, ports.ActivitySignal) error {
		during = h.undelivered()
		return nil
	})
	_ = h.o.ApplyActivitySignal(context.Background(), "w1", ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "chat.turn.completed", ControllerGeneration: "g1",
		FinalAssistantMessage: "Picking up at the HubApp mapping step.", ContextThrash: thrashStop,
	})
	assertContextThrashNotice(t, during)
}

// B738: a chat turn_ended from the durable fallback row becomes available after
// ChatQuietDelay, not the report service's 10 minute default.
func TestChatDurableTurnEndedUsesChatQuietDelay(t *testing.T) {
	h := newDurableHarness(t, domain.SessionModeChat)
	h.complete("done")
	h.now = h.now.Add(time.Minute)
	h.o.deliverPending(context.Background())
	reports := h.autoReports()
	if len(reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(reports))
	}
	if got := reports[0].AvailableAt.Sub(h.now); got != ChatQuietDelay {
		t.Fatalf("available after %v, want %v", got, ChatQuietDelay)
	}
}
