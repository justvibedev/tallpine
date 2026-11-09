package turnnotify

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type applierFunc func(context.Context, domain.SessionID, ports.ActivitySignal) error

func (f applierFunc) ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error {
	return f(ctx, id, s)
}

type closeFallbacks struct {
	pending []domain.TurnFallback
	held    string
}

func (f *closeFallbacks) ListPendingTurnFallbacks(context.Context) ([]domain.TurnFallback, error) {
	return f.pending, nil
}

func (f *closeFallbacks) HasCurrentCompletedTurnFallback(context.Context, domain.SessionID) (bool, error) {
	return false, nil
}

func (f *closeFallbacks) LatestHeldTurnEndedReport(context.Context, domain.SessionID) (string, bool, error) {
	return f.held, f.held != "", nil
}

func (h *harness) undelivered() string {
	return h.o.UndeliveredTurnResult(context.Background(), h.sessions["w1"])
}

// B557: lifecycle closes the worker inside the Stop signal, before the observer
// records the turn end, so the signal's own text must already be readable.
func TestUndeliveredTurnResult_StopSignalThatClosesTheSession(t *testing.T) {
	h := newHarness(domain.KindWorker)
	var during string
	h.o.Next = applierFunc(func(context.Context, domain.SessionID, ports.ActivitySignal) error {
		during = h.undelivered()
		return nil
	})
	h.signal("stop", "the answer to your question")
	if !strings.Contains(during, "the answer to your question") {
		t.Fatalf("during the closing Stop signal got %q", during)
	}
	h.o.mu.Lock()
	left := len(h.o.finishing)
	h.o.mu.Unlock()
	if left != 0 {
		t.Fatalf("finishing entries left after the signal: %d", left)
	}
}

// B557: a close inside the idle window finds the armed notice, and reading it
// leaves that notice armed in case the close does not go through.
func TestUndeliveredTurnResult_ArmedTimerIsReadWithoutDisarming(t *testing.T) {
	h := newHarness(domain.KindWorker)
	h.signal("user-prompt-submit", "")
	h.signal("stop", "armed answer")
	if got := h.undelivered(); !strings.Contains(got, "armed answer") || !strings.HasPrefix(got, "[auto] turn_ended:") {
		t.Fatalf("armed timer result = %q", got)
	}
	if len(h.reports.got) != 0 {
		t.Fatalf("reading must not file a report: %+v", h.reports.got)
	}
	h.fire()
	if len(h.reports.got) != 1 {
		t.Fatalf("normal notice lost after the read: %d reports", len(h.reports.got))
	}
	if got := h.undelivered(); got != "" {
		t.Fatalf("result already sent as a report, still offered: %q", got)
	}
}

func TestUndeliveredTurnResult_NothingToRelay(t *testing.T) {
	running := []string{"sub-1"}
	for name, tc := range map[string]struct {
		before func(*harness)
		signal ports.ActivitySignal
	}{
		"worker filed its own final report": {
			before: func(h *harness) { h.o.ReportFiled(domain.ReportRecord{SessionID: "w1", State: domain.ReportDone}) },
			signal: ports.ActivitySignal{Event: "stop", FinalAssistantMessage: "text"},
		},
		"subagent stop":             {signal: ports.ActivitySignal{Event: "stop", SubagentID: "s", FinalAssistantMessage: "text"}},
		"stop with subagents live":  {signal: ports.ActivitySignal{Event: "stop", RunningSubagentIDs: &running, FinalAssistantMessage: "text"}},
		"turn with no text":         {signal: ports.ActivitySignal{Event: "stop"}},
		"usage limit is not a text": {signal: ports.ActivitySignal{Event: "stop", UsageLimitPausedUntil: time.Now(), FinalAssistantMessage: "text"}},
		"approval acknowledgement": {
			before: func(h *harness) {
				h.o.ApprovalReported(domain.ReportRecord{SessionID: "w1", ID: domain.ReviewApprovedReportIDPrefix + "1", Synthesized: true})
			},
			signal: ports.ActivitySignal{Event: "stop", FinalAssistantMessage: "ack"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(domain.KindWorker)
			h.signal("user-prompt-submit", "")
			if tc.before != nil {
				tc.before(h)
			}
			got := "unset"
			h.o.Next = applierFunc(func(context.Context, domain.SessionID, ports.ActivitySignal) error {
				got = h.undelivered()
				return nil
			})
			if err := h.o.ApplyActivitySignal(context.Background(), "w1", tc.signal); err != nil {
				t.Fatal(err)
			}
			if got != "" {
				t.Fatalf("got %q, want nothing to relay", got)
			}
		})
	}
}

// B557: a durable completion waiting for its due time dies with the session too.
func TestUndeliveredTurnResult_PendingDurableCompletion(t *testing.T) {
	h := newHarness(domain.KindWorker)
	limited := domain.UsageLimitFallbackExcerpt(time.Now(), true)
	f := &closeFallbacks{pending: []domain.TurnFallback{
		{SessionID: "w1", Excerpt: "older"},
		{SessionID: "w1", Excerpt: "durable answer"},
		{SessionID: "w1", Excerpt: limited},
		{SessionID: "other", Excerpt: "not this worker"},
	}}
	h.o.Fallbacks = f
	if got := h.undelivered(); !strings.Contains(got, "durable answer") {
		t.Fatalf("durable result = %q", got)
	}
}

// B557: a filed report still held quiet is discarded once its sender is
// terminated, so it is relayed; one with no quoted text has nothing to add.
func TestUndeliveredTurnResult_HeldQuietReport(t *testing.T) {
	h := newHarness(domain.KindWorker)
	message := "[auto] turn_ended: went idle." + lastMessageLabel + "held answer"
	h.o.Fallbacks = &closeFallbacks{held: message}
	if got := h.undelivered(); got != message {
		t.Fatalf("held report = %q", got)
	}
	h.o.Fallbacks = &closeFallbacks{held: "[auto] turn_ended: went idle."}
	if got := h.undelivered(); got != "" {
		t.Fatalf("textless held report relayed: %q", got)
	}
}

func TestUndeliveredTurnResult_OnlyWorkers(t *testing.T) {
	h := newHarness(domain.KindOrchestrator)
	got := "unset"
	h.o.Next = applierFunc(func(context.Context, domain.SessionID, ports.ActivitySignal) error {
		got = h.undelivered()
		return nil
	})
	h.signal("stop", "text")
	if got != "" {
		t.Fatalf("orchestrator result = %q", got)
	}
}
