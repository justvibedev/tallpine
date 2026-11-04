package sessionmanager

import (
	"context"
	"errors"
	"sync"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type movedRecorder struct {
	mu        sync.Mutex
	harnesses []domain.AgentHarness
}

func (r *movedRecorder) moved(h domain.AgentHarness) {
	r.mu.Lock()
	r.harnesses = append(r.harnesses, h)
	r.mu.Unlock()
}

func (r *movedRecorder) got() []domain.AgentHarness {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domain.AgentHarness(nil), r.harnesses...)
}

func TestAccountMoveCommitNotifiesTheListenerOnce(t *testing.T) {
	t.Parallel()

	manager, store, _, _, _, _ := newAccountTransitionManager(t, domain.SessionModeTUI)
	rec := &movedRecorder{}
	manager.SetAccountMoved(rec.moved)

	transition, err := manager.StartAccountTransition(context.Background(), "session-1", "acct-b", domain.SessionInterfaceTransitionDrain)
	if err != nil {
		t.Fatal(err)
	}
	if settled := awaitTransition(t, store, transition.ID); settled.Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("phase = %s", settled.Phase)
	}
	if got := rec.got(); len(got) != 1 || got[0] != domain.HarnessClaudeCode {
		t.Fatalf("listener heard %v, want one claude-code", got)
	}
}

func TestAccountMoveThatFailsToCommitDoesNotNotify(t *testing.T) {
	t.Parallel()

	manager, store, _, _, accounts, _ := newAccountTransitionManager(t, domain.SessionModeTUI)
	accounts.bindErr = errors.New("database is locked")
	rec := &movedRecorder{}
	manager.SetAccountMoved(rec.moved)

	transition, err := manager.StartAccountTransition(context.Background(), "session-1", "acct-b", domain.SessionInterfaceTransitionDrain)
	if err != nil {
		t.Fatal(err)
	}
	awaitTransition(t, store, transition.ID)
	if got := rec.got(); len(got) != 0 {
		t.Fatalf("listener heard %v for a move that rolled back", got)
	}
}
