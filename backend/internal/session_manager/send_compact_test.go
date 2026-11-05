package sessionmanager

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestCompactIsRawOnlyForKiro(t *testing.T) {
	if !isSlashCommand("/compact", domain.HarnessKiro) {
		t.Fatal("/compact must reach Kiro raw")
	}
	for _, h := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		if isSlashCommand("/compact", h) {
			t.Fatalf("/compact is unverified raw for %s", h)
		}
	}
}

// A Kiro terminal session gets "/compact" typed exactly, not inside a
// session-delivery envelope the TUI would not treat as a command.
func TestPrepareDeliveryMessageSendsKiroCompactRaw(t *testing.T) {
	m, st, _, _ := newManager()
	rec, err := st.CreateSession(context.Background(), domain.SessionRecord{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessKiro, Mode: domain.SessionModeTUI,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _, raw := m.prepareDeliveryMessage(context.Background(), rec.ID, "/compact", "c1", ports.MessageDeliveryOptions{})
	if got != "/compact" || !raw {
		t.Fatalf("got (%q, raw=%v), want an unwrapped raw /compact", got, raw)
	}
}
