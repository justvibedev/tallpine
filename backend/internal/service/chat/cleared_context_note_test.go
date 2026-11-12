package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// A /clear typed in a Terminal UI Claude session keeps the appended system
// prompt but drops the spawn brief and everything else Tallpine sent as
// messages. The note its SessionStart hook adds carries the whole brief, read
// as a clear rather than a switch, and the turns Tallpine recorded. The agent
// is still running, so it is told to wait rather than report back.
func TestClearedContextNoteRebriefsATerminalSession(t *testing.T) {
	t.Parallel()
	h := newHarnessForHarness(t, domain.HarnessClaudeCode)
	ctx := context.Background()
	record, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	record.Mode = domain.SessionModeTUI
	record.Metadata.Prompt = strings.Repeat("brief head. ", 200) + clearBriefRule + strings.Repeat(" brief tail.", 200)
	if err := h.st.UpdateSession(ctx, record); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	at := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	if err := h.st.RecordTerminalTurnRequest(ctx, domain.TerminalTurn{
		SessionID: testSession, Harness: domain.HarnessClaudeCode, UserRequest: "remember the number 42", RequestedAt: at,
	}); err != nil {
		t.Fatalf("record terminal request: %v", err)
	}
	if err := h.st.RecordTerminalTurnResponse(ctx, testSession, "noted", at.Add(time.Minute)); err != nil {
		t.Fatalf("record terminal response: %v", err)
	}

	note, err := h.svc.ClearedContextNote(ctx, testSession)
	if err != nil {
		t.Fatalf("ClearedContextNote: %v", err)
	}
	for _, want := range []string{"cleared your context with /clear", clearBriefRule, "remember the number 42", "Wait for the user's next message"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note is missing %q:\n%s", want, note)
		}
	}
	for _, unwanted := range []string{"taking over this Tallpine session from", "Reply with a few lines"} {
		if strings.Contains(note, unwanted) {
			t.Fatalf("a cleared note must not say %q:\n%s", unwanted, note)
		}
	}
	if _, err := h.svc.ClearedContextNote(ctx, "missing"); !errors.Is(err, ports.ErrSessionNotFound) {
		t.Fatalf("ClearedContextNote(missing) = %v, want ErrSessionNotFound", err)
	}
}
