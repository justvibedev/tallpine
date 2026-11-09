package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func semanticPendingManager(t *testing.T, state domain.ActivityState) (*Manager, *fakeStore, *fakeMessenger) {
	t.Helper()
	st := newFakeStore()
	st.sessions["s1"] = pastStartupGate(domain.SessionRecord{
		ID: "s1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: state},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	})
	msg := &fakeMessenger{}
	m := newSendTestManager(t, semanticSignalingAgent{}, msg, st)
	m.sendConfirm.semanticAcceptance = time.Millisecond
	return m, st, msg
}

func setSemanticSession(st *fakeStore, change func(*domain.SessionRecord)) {
	rec := st.sessions["s1"]
	change(&rec)
	st.sessions["s1"] = rec
}

// B545: a terminal orchestrator mid-turn has not failed; its queued report is
// accepted when the turn ends.
func TestSendSemanticMidTurnTimeoutIsPendingNotFailure(t *testing.T) {
	t.Parallel()
	m, _, msg := semanticPendingManager(t, domain.ActivityActive)

	err := m.SendSemantic(context.Background(), "s1", "report", "report-batch:b")
	if !errors.Is(err, ErrSemanticAcceptancePending) || errors.Is(err, ErrSemanticAcceptanceUnsupported) {
		t.Fatalf("SendSemantic error = %v, want only ErrSemanticAcceptancePending", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("pane writes = %d, want 1", len(msg.msgs))
	}
}

// B545: an idle agent that ignored the prompt is a real failure and must keep
// backing off, or an unresponsive pane would be typed into every busy retry.
func TestSendSemanticIdleTimeoutStaysAFailure(t *testing.T) {
	t.Parallel()
	m, _, _ := semanticPendingManager(t, domain.ActivityIdle)

	err := m.SendSemantic(context.Background(), "s1", "report", "report-batch:b")
	if !errors.Is(err, ErrSemanticAcceptanceUnsupported) || errors.Is(err, ErrSemanticAcceptancePending) {
		t.Fatalf("SendSemantic error = %v, want only ErrSemanticAcceptanceUnsupported", err)
	}
}

// B545: retries during one running turn must not repeat the batch to the agent.
// When that turn ends, one more paste recovers a queued prompt that was lost.
func TestSendSemanticDoesNotRetypeDuringRunningTurn(t *testing.T) {
	t.Parallel()
	m, st, msg := semanticPendingManager(t, domain.ActivityActive)
	ctx := context.Background()
	if err := m.SendSemantic(ctx, "s1", "report", "report-batch:b"); !errors.Is(err, ErrSemanticAcceptancePending) {
		t.Fatalf("first SendSemantic error = %v", err)
	}

	// The retry must also return at once: waiting here would hold the single
	// report delivery loop for the whole acceptance window, every 30s.
	m.sendConfirm.semanticAcceptance = time.Hour
	retryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := m.SendSemantic(retryCtx, "s1", "report", "report-batch:b"); !errors.Is(err, ErrSemanticAcceptancePending) {
		t.Fatalf("retry SendSemantic error = %v", err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("pane writes during the turn = %d, want 1", len(msg.msgs))
	}

	m.sendConfirm.semanticAcceptance = time.Millisecond
	setSemanticSession(st, func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityIdle })
	if err := m.SendSemantic(ctx, "s1", "report", "report-batch:b"); !errors.Is(err, ErrSemanticAcceptanceUnsupported) {
		t.Fatalf("idle SendSemantic error = %v", err)
	}
	// The idle path also sends Enter-only nudges; only pastes of the batch count.
	if got := batchPastes(msg.msgs); got != 2 {
		t.Fatalf("batch pastes after the turn = %d of %q, want 2", got, msg.msgs)
	}
}

// B545: a relaunched agent never saw the earlier paste, so it must be typed again.
func TestSendSemanticRetypesAfterRelaunch(t *testing.T) {
	t.Parallel()
	m, st, msg := semanticPendingManager(t, domain.ActivityActive)
	ctx := context.Background()
	if err := m.SendSemantic(ctx, "s1", "report", "report-batch:b"); !errors.Is(err, ErrSemanticAcceptancePending) {
		t.Fatalf("first SendSemantic error = %v", err)
	}

	setSemanticSession(st, func(r *domain.SessionRecord) { r.Metadata.RuntimeLaunchID = "launch-2" })
	if err := m.SendSemantic(ctx, "s1", "report", "report-batch:b"); !errors.Is(err, ErrSemanticAcceptancePending) {
		t.Fatalf("relaunch SendSemantic error = %v", err)
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("pane writes = %d, want 2", len(msg.msgs))
	}
}

func acceptSemantic(r *domain.SessionRecord, key string) {
	r.Metadata.ConversationCheckpointState = domain.ConversationCheckpointCoordination
	r.Metadata.ConversationCheckpointGeneration = r.Metadata.RuntimeLaunchID
	r.Metadata.ConversationCheckpointTurnID = key
}

func batchPastes(msgs []string) int {
	pastes := 0
	for _, m := range msgs {
		if m != "" {
			pastes++
		}
	}
	return pastes
}

// B545 review: two batches of one orchestrator must not evict each other, and
// accepting one must leave the other's record intact.
func TestSendSemanticTracksEachBatchDuringRunningTurn(t *testing.T) {
	t.Parallel()
	m, st, msg := semanticPendingManager(t, domain.ActivityActive)
	ctx := context.Background()
	for _, key := range []string{"report-batch:a", "report-batch:b", "report-batch:a", "report-batch:b"} {
		if err := m.SendSemantic(ctx, "s1", "report", key); !errors.Is(err, ErrSemanticAcceptancePending) {
			t.Fatalf("SendSemantic(%s) error = %v", key, err)
		}
	}
	if got := batchPastes(msg.msgs); got != 2 {
		t.Fatalf("batch pastes = %d, want one per batch", got)
	}

	setSemanticSession(st, func(r *domain.SessionRecord) { acceptSemantic(r, "report-batch:a") })
	if err := m.SendSemantic(ctx, "s1", "report", "report-batch:a"); err != nil {
		t.Fatalf("accepted SendSemantic: %v", err)
	}
	if err := m.SendSemantic(ctx, "s1", "report", "report-batch:b"); !errors.Is(err, ErrSemanticAcceptancePending) {
		t.Fatalf("other batch error = %v", err)
	}
	if got := batchPastes(msg.msgs); got != 2 {
		t.Fatalf("batch pastes after accepting one = %d, want 2", got)
	}
}

// scriptedSessionStore changes the session on each read, standing in for hook
// signals that land while SendSemantic polls.
type scriptedSessionStore struct {
	*fakeStore
	reads  int
	script func(read int, r *domain.SessionRecord)
}

func (s *scriptedSessionStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	s.reads++
	setSemanticSession(s.fakeStore, func(r *domain.SessionRecord) { s.script(s.reads, r) })
	return s.fakeStore.GetSession(ctx, id)
}

func typedThenIdle(t *testing.T, script func(int, *domain.SessionRecord)) (*Manager, *fakeMessenger) {
	t.Helper()
	m, st, msg := semanticPendingManager(t, domain.ActivityActive)
	if err := m.SendSemantic(context.Background(), "s1", "report", "report-batch:b"); !errors.Is(err, ErrSemanticAcceptancePending) {
		t.Fatalf("first SendSemantic error = %v", err)
	}
	setSemanticSession(st, func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityIdle })
	m.sendConfirm.semanticAcceptance = 5 * time.Second
	m.store = &scriptedSessionStore{fakeStore: st, script: script}
	return m, msg
}

// B545 review: the turn-end signal runs the retry just as the agent drains its
// queued prompt, so the copy already typed gets to be accepted first.
func TestSendSemanticWaitsForTypedCopyAfterTurnEnds(t *testing.T) {
	t.Parallel()
	m, msg := typedThenIdle(t, func(read int, r *domain.SessionRecord) {
		if read >= 3 {
			acceptSemantic(r, "report-batch:b")
		}
	})

	if err := m.SendSemantic(context.Background(), "s1", "report", "report-batch:b"); err != nil {
		t.Fatalf("SendSemantic: %v", err)
	}
	if got := batchPastes(msg.msgs); got != 1 {
		t.Fatalf("batch pastes = %d of %q, want the single typed copy", got, msg.msgs)
	}
}

// B545 review: a turn that starts during that wait is still draining input, so
// typing another copy would queue a duplicate.
func TestSendSemanticDoesNotRetypeWhenNextTurnStartsDuringWait(t *testing.T) {
	t.Parallel()
	m, msg := typedThenIdle(t, func(read int, r *domain.SessionRecord) {
		if read >= 2 {
			r.Activity.State = domain.ActivityActive
		}
	})
	m.sendConfirm.semanticAcceptance = 5 * time.Millisecond

	err := m.SendSemantic(context.Background(), "s1", "report", "report-batch:b")
	if !errors.Is(err, ErrSemanticAcceptancePending) {
		t.Fatalf("SendSemantic error = %v", err)
	}
	if got := batchPastes(msg.msgs); got != 1 {
		t.Fatalf("batch pastes = %d, want 1", got)
	}
}

// B545 review: a permission dialog keeps the turn open for as long as nobody
// answers, so a typed batch must not hold the delivery loop on each retry.
func TestSendSemanticBlockedTurnReturnsPendingWithoutWaiting(t *testing.T) {
	t.Parallel()
	m, st, msg := semanticPendingManager(t, domain.ActivityActive)
	ctx := context.Background()
	if err := m.SendSemantic(ctx, "s1", "report", "report-batch:b"); !errors.Is(err, ErrSemanticAcceptancePending) {
		t.Fatalf("first SendSemantic error = %v", err)
	}
	setSemanticSession(st, func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityBlocked })

	m.sendConfirm.semanticAcceptance = time.Hour
	retryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := m.SendSemantic(retryCtx, "s1", "report", "report-batch:b"); !errors.Is(err, ErrSemanticAcceptancePending) {
		t.Fatalf("blocked retry error = %v", err)
	}
	if got := batchPastes(msg.msgs); got != 1 {
		t.Fatalf("batch pastes = %d, want 1", got)
	}
}

// B545 review: a turn that stops on a decision while the wait runs is unfinished
// too; neither timeout may count it as a delivery failure.
func TestSendSemanticTurnBlockingDuringWaitStaysPending(t *testing.T) {
	t.Parallel()
	blockFrom := func(from int) func(int, *domain.SessionRecord) {
		return func(read int, r *domain.SessionRecord) {
			if read >= from {
				r.Activity.State = domain.ActivityBlocked
			}
		}
	}

	t.Run("after the first paste", func(t *testing.T) {
		m, st, msg := semanticPendingManager(t, domain.ActivityActive)
		m.store = &scriptedSessionStore{fakeStore: st, script: func(_ int, r *domain.SessionRecord) {
			if batchPastes(msg.msgs) > 0 {
				r.Activity.State = domain.ActivityBlocked
			}
		}}
		err := m.SendSemantic(context.Background(), "s1", "report", "report-batch:b")
		if !errors.Is(err, ErrSemanticAcceptancePending) {
			t.Fatalf("SendSemantic error = %v", err)
		}
		if got := batchPastes(msg.msgs); got != 1 {
			t.Fatalf("batch pastes = %d, want 1", got)
		}
	})

	t.Run("before a re-paste", func(t *testing.T) {
		m, msg := typedThenIdle(t, blockFrom(2))
		m.sendConfirm.semanticAcceptance = 5 * time.Millisecond
		err := m.SendSemantic(context.Background(), "s1", "report", "report-batch:b")
		if !errors.Is(err, ErrSemanticAcceptancePending) {
			t.Fatalf("SendSemantic error = %v", err)
		}
		if got := batchPastes(msg.msgs); got != 1 {
			t.Fatalf("batch pastes = %d, want 1", got)
		}
	})
}
