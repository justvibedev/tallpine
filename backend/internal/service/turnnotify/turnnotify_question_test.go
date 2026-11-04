package turnnotify

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// A worker blocked on an in-chat question tells its orchestrator the request id
// and how to answer it (B443: sessions sat 5-13 minutes on an unseen form).
func TestInputQuestionReportCarriesRequestIDAndAnswerCommand(t *testing.T) {
	h := chatHarness()
	_ = h.o.ApplyActivitySignal(context.Background(), "w1", ports.ActivitySignal{
		Valid: true, State: domain.ActivityWaitingInput, Event: "chat.input.requested", ControllerGeneration: "g1",
		Waiting: "Switch to Opus 4.8?", ApprovalRequestID: "in-1",
	})
	n := h.reports.got[0].Note
	if !strings.Contains(n, "Request id: in-1") || !strings.Contains(n, "session answer <session> <requestId>") {
		t.Fatalf("note = %q", n)
	}
}
