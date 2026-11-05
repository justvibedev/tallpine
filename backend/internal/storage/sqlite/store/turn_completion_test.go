package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B558: restart notices are judged against when a turn last ended normally.
// An interrupted (restart-cut) turn must not count as a completed one.
func TestLastCompletedTurnAtIgnoresInterruptedTurns(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	session, _ := seedAccountAutoResume(t, s, domain.HarnessClaudeCode)
	turnID := "turn-" + string(session.ID)

	mustNoError(t, s.SettleTurnByID(ctx, turnID, domain.TurnStateInterrupted, "", testNow.Add(time.Minute)))
	if _, ok, err := s.LastCompletedTurnAt(ctx, session.ID); err != nil || ok {
		t.Fatalf("interrupted turn counted as completed: ok=%v err=%v", ok, err)
	}

	mustNoError(t, s.SettleTurnByID(ctx, turnID, domain.TurnStateCompleted, "", testNow.Add(2*time.Minute)))
	at, ok, err := s.LastCompletedTurnAt(ctx, session.ID)
	if err != nil || !ok || !at.Equal(testNow.Add(time.Minute)) {
		t.Fatalf("completed turn: at=%v ok=%v err=%v", at, ok, err)
	}
}
