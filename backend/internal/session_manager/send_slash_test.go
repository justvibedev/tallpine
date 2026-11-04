package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestIsSlashCommand(t *testing.T) {
	for _, tc := range []struct {
		msg     string
		harness domain.AgentHarness
		want    bool
	}{
		{"/context", domain.HarnessClaudeCode, true},
		{" /cost ", domain.HarnessClaudeCode, true},
		{"/usage", domain.HarnessClaudeCode, false}, // modal in claude-code
		{"/usage", "kiro", true},
		{"/model opus", domain.HarnessClaudeCode, false},
		{"/config", "kiro", false},
		{"/review", "kiro", false},
		{"/tmp", "kiro", false},
		{"/context\nmore", "kiro", false},
		{"hello", "kiro", false},
		{"/", "kiro", false},
	} {
		if got := isSlashCommand(tc.msg, tc.harness); got != tc.want {
			t.Errorf("isSlashCommand(%q,%s)=%v want %v", tc.msg, tc.harness, got, tc.want)
		}
	}
}

// A raw slash command is delivered once, unwrapped, with no confirmation nudges.
func TestSend_SlashCommandDeliveredRawWithoutConfirmation(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{ID: "s1", Harness: "claude-code",
		Activity: domain.Activity{State: domain.ActivityIdle}})
	msg := &fakeMessenger{}
	m := newSendTestManager(t, signalingAgent{}, msg, st)

	if err := m.Send(context.Background(), "s1", "/context", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(msg.msgs) != 1 || msg.msgs[0] != "/context" {
		t.Fatalf("messages = %q, want exactly one raw /context", msg.msgs)
	}
}

func slashSendFixture(t *testing.T) (*Manager, *fakeMessenger) {
	t.Helper()
	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{ID: "s1", Harness: "claude-code",
		Activity: domain.Activity{State: domain.ActivityIdle}})
	msg := &fakeMessenger{}
	return newSendTestManager(t, signalingAgent{}, msg, st), msg
}

// A user-authored slash command keeps delivery confirmation.
func TestSend_UserAuthoredSlashCommandStillConfirms(t *testing.T) {
	t.Parallel()
	m, _ := slashSendFixture(t)
	err := m.SendWithOptions(context.Background(), "s1", "/context", nil, ports.MessageDeliveryOptions{AuthoredByUser: true})
	if !errors.Is(err, ErrDeliveryUnconfirmed) {
		t.Fatalf("err = %v, want ErrDeliveryUnconfirmed", err)
	}
}

// Paths and multi-line messages are still wrapped; a raw send gets a receipt ID.
func TestSend_NonCommandsStayWrappedAndRawSendHasReceiptID(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"/tmp", "/tmp/file.go look", "/context\nand more"} {
		m, msg := slashSendFixture(t)
		_ = m.Send(context.Background(), "s1", text, nil)
		if len(msg.msgs) == 0 || !strings.Contains(msg.msgs[0], "ao-session-delivery") {
			t.Fatalf("%q was not wrapped: %q", text, msg.msgs)
		}
	}
	m, msg := slashSendFixture(t)
	receipt, err := m.SendWithReceipt(context.Background(), "s1", "/context", nil, ports.MessageDeliveryOptions{})
	if err != nil || receipt.ID == "" || len(msg.msgs) != 1 || msg.msgs[0] != "/context" {
		t.Fatalf("receipt=%+v err=%v msgs=%q, want raw send with receipt ID", receipt, err, msg.msgs)
	}
}

// Surrounding whitespace is stripped from a raw slash command.
func TestSend_RawSlashCommandIsTrimmed(t *testing.T) {
	t.Parallel()
	m, msg := slashSendFixture(t)
	if err := m.Send(context.Background(), "s1", " /context \n", nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(msg.msgs) != 1 || msg.msgs[0] != "/context" {
		t.Fatalf("messages = %q, want exactly %q", msg.msgs, "/context")
	}
}

func orchSlashFixture(t *testing.T, senderKind domain.SessionKind, senderProject domain.ProjectID) (*Manager, *fakeMessenger) {
	t.Helper()
	st := newFakeStore()
	st.sessions["w1"] = pastStartupGate(domain.SessionRecord{ID: "w1", ProjectID: "p1", Harness: "kiro",
		Activity: domain.Activity{State: domain.ActivityIdle}})
	st.sessions["o1"] = domain.SessionRecord{ID: "o1", ProjectID: senderProject, Kind: senderKind, Harness: "claude-code"}
	msg := &fakeMessenger{}
	return newSendTestManager(t, signalingAgent{}, msg, st), msg
}

// An orchestrator's "[from o1] /usage" to a worker of its own project reaches
// the harness as the bare command.
func TestSend_OrchestratorSlashToOwnWorkerIsRaw(t *testing.T) {
	t.Parallel()
	m, msg := orchSlashFixture(t, domain.KindOrchestrator, "p1")
	err := m.SendWithOptions(context.Background(), "w1", "[from o1] /usage", nil, ports.MessageDeliveryOptions{SenderSessionID: "o1"})
	if err != nil || len(msg.msgs) != 1 || msg.msgs[0] != "/usage" {
		t.Fatalf("err=%v msgs=%q, want one raw /usage", err, msg.msgs)
	}
}

// Other senders, other projects and non-allowlisted commands stay wrapped.
func TestSend_OtherSlashSendsStayWrapped(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		kind    domain.SessionKind
		project domain.ProjectID
		text    string
	}{
		{"worker sender", domain.KindWorker, "p1", "[from o1] /usage"},
		{"other project", domain.KindOrchestrator, "p2", "[from o1] /usage"},
		{"not allowlisted", domain.KindOrchestrator, "p1", "[from o1] /clear"},
	}
	for _, tc := range cases {
		m, msg := orchSlashFixture(t, tc.kind, tc.project)
		_ = m.SendWithOptions(context.Background(), "w1", tc.text, nil, ports.MessageDeliveryOptions{SenderSessionID: "o1"})
		if len(msg.msgs) == 0 || !strings.Contains(msg.msgs[0], "ao-session-delivery") || !strings.Contains(msg.msgs[0], "[from o1]") {
			t.Errorf("%s: not wrapped with prefix: %q", tc.name, msg.msgs)
		}
	}
}
