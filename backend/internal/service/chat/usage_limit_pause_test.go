package chat

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type pauseTurnStore struct {
	Store
	turn domain.ConversationTurn
}

func (s pauseTurnStore) LatestAutoResumeTurn(context.Context, string) (domain.ConversationTurn, error) {
	return s.turn, nil
}

func TestControllerUsageLimitPausedUntil(t *testing.T) {
	failed := time.Now().Add(-time.Minute)
	reset := time.Now().Add(time.Hour)
	turn := domain.ConversationTurn{State: domain.TurnStateFailed, CompletedAt: &failed, UsageLimit: &domain.ConversationUsageLimit{ResetsAt: &reset}}
	c := &Controller{store: pauseTurnStore{turn: turn}, now: time.Now}
	if until, paused := c.usageLimitPausedUntil(context.Background()); !paused || !until.Equal(reset) {
		t.Fatalf("paused=%v until=%v, want reset %v", paused, until, reset)
	}
	past := time.Now().Add(-time.Second)
	turn.UsageLimit = &domain.ConversationUsageLimit{ResetsAt: &past}
	c.store = pauseTurnStore{turn: turn}
	if _, paused := c.usageLimitPausedUntil(context.Background()); paused {
		t.Fatal("paused after reset")
	}
	turn.UsageLimit = nil
	c.store = pauseTurnStore{turn: turn}
	if _, paused := c.usageLimitPausedUntil(context.Background()); paused {
		t.Fatal("paused for an ordinary failure")
	}
}

func TestPauseWakeIsCancelledByCloseAndNotRearmed(t *testing.T) {
	c := &Controller{now: time.Now}
	fired := make(chan struct{}, 1)
	c.pauseWake = time.AfterFunc(50*time.Millisecond, func() { fired <- struct{}{} })
	c.mu.Lock()
	c.explicitClose = true
	c.stopPauseWakeLocked()
	c.mu.Unlock()
	c.armPauseWake(time.Now().Add(-time.Hour))
	select {
	case <-fired:
		t.Fatal("wake timer fired after close")
	case <-time.After(300 * time.Millisecond):
	}
	if c.pauseWake != nil {
		t.Fatal("a closed controller armed a new wake")
	}
}
