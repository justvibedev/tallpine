package chat_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func approvalSignal(t *testing.T, detail string) ports.ActivitySignal {
	t.Helper()
	h := newHarness(t)
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "go", ClientMessageID: "c1"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventApprovalRequested, RequestID: "req-7", Summary: "Run command", Detail: []byte(detail)})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range h.activity.snapshot() {
			if s.State == domain.ActivityWaitingInput && s.Event == "chat.approval.requested" {
				return s
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no approval signal")
	return ports.ActivitySignal{}
}

// The signal the orchestrator notice is built from carries the exact command
// and the request id, from the event the drivers actually emit.
func TestApprovalSignalCarriesTheExactCommand(t *testing.T) {
	t.Parallel()
	s := approvalSignal(t, `{"reason":"needs tmp","command":"go build","rawCommand":"/bin/zsh -lc 'go build /tmp/x'"}`)
	if s.ApprovalCommand != "/bin/zsh -lc 'go build /tmp/x'" || s.ApprovalRequestID != "req-7" {
		t.Fatalf("signal = %+v", s)
	}
}

// A long command is cut, and a file edit's content never rides along.
func TestApprovalSignalBoundsTheCommandAndNeverCarriesFileContent(t *testing.T) {
	t.Parallel()
	s := approvalSignal(t, `{"command":"`+strings.Repeat("c", 5000)+`","input":{"file_path":"/w/a.go","content":"top-secret-file"}}`)
	if n := len([]rune(s.ApprovalCommand)); n > 501 || n < 500 {
		t.Fatalf("command runes = %d, want 500 plus an ellipsis", n)
	}
	for _, field := range []string{s.ApprovalCommand, s.Waiting, s.ToolName} {
		if strings.Contains(field, "top-secret-file") {
			t.Fatalf("file content leaked: %q", field)
		}
	}
	none := approvalSignal(t, `{"input":{"file_path":"/w/a.go","content":"top-secret-file"}}`)
	if none.ApprovalCommand != "" {
		t.Fatalf("a file change produced a command: %q", none.ApprovalCommand)
	}
}
