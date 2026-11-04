package chat_test

import (
	"context"
	"testing"
)

// requireArmed fails unless the turn's auto-resume consent equals want.
func requireArmed(t *testing.T, h *harness, turnID string, want bool) {
	t.Helper()
	turn, err := h.st.TurnByID(context.Background(), turnID)
	if err != nil || turn.UsageLimit == nil || turn.UsageLimit.AutoResume != want {
		t.Fatalf("turn %s = %+v, %v, want armed=%v", turnID, turn, err, want)
	}
}
