package restartresume

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

type fakeTurnLog struct {
	*fakeStore
	at time.Time
}

func (f fakeTurnLog) LastCompletedTurnAt(context.Context, domain.SessionID) (time.Time, bool, error) {
	return f.at, !f.at.IsZero(), nil
}

// B558: the orchestrator was idle again after completed turns when the notice
// arrived. Activity timestamps are bumped by the restart itself, so only a
// finished turn proves the cut-off is over.
func TestResumeDropsNoticeForIdleSessionThatCompletedATurnSinceRestart(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		turnAt   func(interrupted time.Time) time.Time
		wantSent int
	}{
		"completed after the restart": {func(i time.Time) time.Time { return i.Add(time.Minute) }, 0},
		"completed before the cut":    {func(i time.Time) time.Time { return i.Add(-time.Minute) }, 1},
		"no completed turn":           {func(time.Time) time.Time { return time.Time{} }, 1},
	} {
		t.Run(name, func(t *testing.T) {
			idle := domain.SessionRecord{ID: "orch", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now()}}
			tr, st := newTracker(t, idle)
			interrupted := time.Now().Add(-time.Hour)
			if err := tr.save([]Entry{{SessionID: "orch", InterruptedAt: interrupted}}); err != nil {
				t.Fatal(err)
			}
			tr.store = fakeTurnLog{fakeStore: st, at: tc.turnAt(interrupted)}
			sender := &fakeSender{}
			tr.Resume(ctx, sender)
			if got := len(sender.sent["orch"]); got != tc.wantSent {
				t.Fatalf("sent %d notices, want %d", got, tc.wantSent)
			}
		})
	}
}

// The production store must provide the turn-end signal.
var _ TurnLog = (*sqlite.Store)(nil)
