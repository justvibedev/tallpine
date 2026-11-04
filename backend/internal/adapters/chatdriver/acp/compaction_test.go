package acp

import (
	"context"
	"encoding/json"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func openCompactionConversation(t *testing.T, agent *fakeAgent) *conversation {
	t.Helper()
	driver := newFakeDriver(agent)
	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	return opened.(*conversation)
}

func TestSetAutoCompactWindowSendsTokensAndReportsWhetherItApplied(t *testing.T) {
	agent := &fakeAgent{compactSupported: true, compactApplied: true}
	conv := openCompactionConversation(t, agent)

	applied, err := conv.SetAutoCompactWindow(context.Background(), 400_000)
	if err != nil || !applied {
		t.Fatalf("applied = %v, err = %v; want applied", applied, err)
	}
	// 0 clears the live value: JSON null, which the bridge hands to the SDK.
	if applied, err = conv.SetAutoCompactWindow(context.Background(), 0); err != nil || !applied {
		t.Fatalf("clear: applied = %v, err = %v", applied, err)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.compactCalls) != 2 {
		t.Fatalf("calls = %d, want 2", len(agent.compactCalls))
	}
	var first, second struct {
		SessionID string `json:"sessionId"`
		Tokens    any    `json:"tokens"`
	}
	_ = json.Unmarshal(agent.compactCalls[0], &first)
	_ = json.Unmarshal(agent.compactCalls[1], &second)
	if first.SessionID == "" || first.Tokens != float64(400_000) || second.Tokens != nil {
		t.Fatalf("payloads = %+v / %+v, want a session id with 400000, then null", first, second)
	}
}

// A bridge that has not been patched, or that cannot move the value, is not an
// error: the choice waits for the next restart.
func TestSetAutoCompactWindowOnAnUnpatchedBridgeIsNotAnError(t *testing.T) {
	conv := openCompactionConversation(t, &fakeAgent{compactSupported: false})
	if applied, err := conv.SetAutoCompactWindow(context.Background(), 400_000); err != nil || applied {
		t.Fatalf("unpatched: applied = %v, err = %v; want false, nil", applied, err)
	}
	declined := openCompactionConversation(t, &fakeAgent{compactSupported: true, compactApplied: false})
	if applied, err := declined.SetAutoCompactWindow(context.Background(), 400_000); err != nil || applied {
		t.Fatalf("locked: applied = %v, err = %v; want false, nil", applied, err)
	}
}

func TestSetAutoCompactWindowOnAClosedConversationErrors(t *testing.T) {
	conv := openCompactionConversation(t, &fakeAgent{compactSupported: true, compactApplied: true})
	_ = conv.Close()
	if _, err := conv.SetAutoCompactWindow(context.Background(), 400_000); err == nil {
		t.Fatal("want an error on a closed conversation")
	}
}
