package chat_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

type fakeFailover struct {
	mu       sync.Mutex
	disabled bool
	starts   int
	moveID   string
	finished bool
	moved    bool
}

func (f *fakeFailover) Enabled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.disabled
}

func (f *fakeFailover) Failover(context.Context, domain.SessionID, domain.AgentHarness) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	return f.moveID
}

func (f *fakeFailover) MoveFinished(context.Context, string) (bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.finished, f.moved
}

func TestUsageLimitFailoverRetriesOnNewAccountBeforeReset(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	source := failAutoResumeTurnOnAccountA(ctx, t, h)
	f := &fakeFailover{moveID: "move-1"}
	h.svc.SetUsageLimitFailover(f)

	// The move is started, but nothing is retried while it runs.
	for range 3 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if f.starts != 1 || len(h.conv.sentMessages()) != 1 {
		t.Fatalf("starts=%d sent=%d, want one move and no retry yet", f.starts, len(h.conv.sentMessages()))
	}
	// A rebind leaves the turn armed (B476); the retry does not need consent.
	if err := h.st.BindSessionAgentAccount(ctx, testSession, domain.AgentAccountProviderCodex, "account-b", h.now()); err != nil {
		t.Fatal(err)
	}
	requireArmed(t, h, source.ID, true)
	f.mu.Lock()
	f.finished, f.moved = true, true
	f.mu.Unlock()
	for range 2 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(h.conv.sentMessages()); got != 2 {
		t.Fatalf("provider received %d messages, want exactly one retry after the move", got)
	}
	requireArmed(t, h, source.ID, false) // the finished move disarmed the source
}

func TestUsageLimitFailoverFailedMoveKeepsWaitingForReset(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	failAutoResumeTurn(ctx, t, h)
	f := &fakeFailover{moveID: "move-1", finished: true, moved: false}
	h.svc.SetUsageLimitFailover(f)
	for range 3 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.conv.sentMessages()) != 1 {
		t.Fatal("a failed move still retried before the reset")
	}
	h.advance(time.Minute)
	for range 2 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.conv.sentMessages()) != 2 {
		t.Fatalf("provider received %d messages, want the ordinary retry at the reset", len(h.conv.sentMessages()))
	}
}

func TestUsageLimitWithoutFailoverNeverMoves(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	failAutoResumeTurn(ctx, t, h)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.conv.sentMessages()) != 1 {
		t.Fatal("retried before the reset without failover")
	}
}

func TestUsageLimitFailoverWithNoAccountIsNotAskedEverySecond(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	failAutoResumeTurn(ctx, t, h)
	f := &fakeFailover{} // no account to move to
	h.svc.SetUsageLimitFailover(f)
	for range 5 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	got := f.starts
	f.mu.Unlock()
	if got != 1 {
		t.Fatalf("failover asked %d times in a burst of scans, want 1", got)
	}
}

type flakyRetryPromptStore struct {
	*sqlite.Store
	mu       sync.Mutex
	failNext bool
}

func (s *flakyRetryPromptStore) RetryPrompt(ctx context.Context, conversationID, turnID string) (domain.RetryPrompt, error) {
	s.mu.Lock()
	fail := s.failNext
	s.failNext = false
	s.mu.Unlock()
	if fail {
		return domain.RetryPrompt{}, errors.New("database is locked")
	}
	return s.Store.RetryPrompt(ctx, conversationID, turnID)
}

func TestUsageLimitFailoverRetryFailingOncePassinglyIsDeliveredLater(t *testing.T) {
	t.Parallel()
	var wrapped *flakyRetryPromptStore
	h := newHarnessWithConversationAndStore(t, nil, func(st *sqlite.Store) chatsvc.Store {
		wrapped = &flakyRetryPromptStore{Store: st}
		return wrapped
	})
	ctx := context.Background()
	failAutoResumeTurnOnAccountA(ctx, t, h)
	f := &fakeFailover{moveID: "move-1"}
	h.svc.SetUsageLimitFailover(f)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	wrapped.mu.Lock()
	wrapped.failNext = true
	wrapped.mu.Unlock()
	if err := h.st.BindSessionAgentAccount(ctx, testSession, domain.AgentAccountProviderCodex, "account-b", h.now()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.finished, f.moved = true, true
	f.mu.Unlock()
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.conv.sentMessages()) != 1 {
		t.Fatal("retry delivered despite the failed read")
	}
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.conv.sentMessages()) != 1 {
		t.Fatal("retry repeated inside the backoff")
	}
	h.advance(time.Minute) // past the backoff
	for range 2 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(h.conv.sentMessages()); got != 2 {
		t.Fatalf("provider received %d messages, want one retry delivered by a later scan", got)
	}
}

type alwaysFailRetryPromptStore struct {
	*sqlite.Store
	mu    sync.Mutex
	on    bool
	calls int
}

func (s *alwaysFailRetryPromptStore) RetryPrompt(ctx context.Context, conversationID, turnID string) (domain.RetryPrompt, error) {
	s.mu.Lock()
	on := s.on
	if on {
		s.calls++
	}
	s.mu.Unlock()
	if on {
		return domain.RetryPrompt{}, errors.New("database is locked")
	}
	return s.Store.RetryPrompt(ctx, conversationID, turnID)
}

func TestUsageLimitFailoverRetryGivesUpAfterFiveAttempts(t *testing.T) {
	t.Parallel()
	var wrapped *alwaysFailRetryPromptStore
	h := newHarnessWithConversationAndStore(t, nil, func(st *sqlite.Store) chatsvc.Store {
		wrapped = &alwaysFailRetryPromptStore{Store: st}
		return wrapped
	})
	ctx := context.Background()
	src := failAutoResumeTurnOnAccountA(ctx, t, h)
	f := &fakeFailover{moveID: "move-1"}
	h.svc.SetUsageLimitFailover(f)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.st.BindSessionAgentAccount(ctx, testSession, domain.AgentAccountProviderCodex, "account-b", h.now()); err != nil {
		t.Fatal(err)
	}
	wrapped.mu.Lock()
	wrapped.on = true
	wrapped.mu.Unlock()
	f.mu.Lock()
	f.finished, f.moved = true, true
	f.mu.Unlock()
	for range 12 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
		h.advance(time.Minute) // past each backoff
	}
	wrapped.mu.Lock()
	calls := wrapped.calls
	wrapped.mu.Unlock()
	if calls != 5 {
		t.Fatalf("retry attempted %d times after the move, want exactly 5 then give up", calls)
	}
	requireArmed(t, h, src.ID, false) // the finished move disarmed the source
	if len(h.conv.sentMessages()) != 1 {
		t.Fatal("a retry was delivered although every attempt failed")
	}
}

func TestUsageLimitFailoverRetrySurvivesADaemonRestart(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	failAutoResumeTurnOnAccountA(ctx, t, h)
	h.svc.SetUsageLimitFailover(&fakeFailover{moveID: "move-1"})
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	// The move commits (rebind disarms the turn), then the daemon restarts
	// before the retry ran.
	if err := h.st.BindSessionAgentAccount(ctx, testSession, domain.AgentAccountProviderCodex, "account-b", h.now()); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	project, _, err := h.st.GetProject(ctx, string(testProject))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(project.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	provider := newFakeConversation()
	provider.turnSeq = 1
	restarted := chatsvc.New(chatsvc.Options{
		Store: reopened, Reader: fullSnapshotReader(reopened), Sessions: reopened,
		Drivers: fakeRegistry{driver: fakeDriver{conv: provider}}, NewID: uuid.NewString, Now: h.now,
	})
	if _, err := restarted.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Stop(ctx, testSession) })
	restarted.SetUsageLimitFailover(&fakeFailover{moveID: "move-1", finished: true, moved: true})
	for range 2 {
		if err := restarted.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(provider.sentMessages()); got != 1 {
		t.Fatalf("restarted daemon delivered %d retries, want exactly 1", got)
	}
	if left, err := reopened.ListAccountFailoverRetries(ctx); err != nil || len(left) != 0 {
		t.Fatalf("records left after delivery = %+v, %v", left, err)
	}
}

func TestUsageLimitFailoverDisabledNeverAsksToMove(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	failAutoResumeTurn(ctx, t, h)
	f := &fakeFailover{moveID: "move-1", disabled: true}
	h.svc.SetUsageLimitFailover(f)
	for range 3 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	starts := f.starts
	f.mu.Unlock()
	if starts != 0 {
		t.Fatalf("a disabled failover was asked %d times", starts)
	}
}

// lockProbeFailover asks the service for its write lock from inside Enabled,
// which cannot succeed while the scan holds the service lock around the call.
type lockProbeFailover struct {
	fakeFailover
	svc      *chatsvc.Service
	released chan struct{}
	once     sync.Once
	blocked  bool
}

func (f *lockProbeFailover) Enabled() bool {
	f.once.Do(func() {
		go func() {
			f.svc.SetUsageLimitFailover(f)
			close(f.released)
		}()
		select {
		case <-f.released:
		case <-time.After(500 * time.Millisecond):
			f.blocked = true
		}
	})
	return true
}

func TestUsageLimitFailoverEnabledIsAskedWithoutServiceLocks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	failAutoResumeTurn(ctx, t, h)
	f := &lockProbeFailover{svc: h.svc, released: make(chan struct{})}
	h.svc.SetUsageLimitFailover(f)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if f.blocked {
		t.Fatal("Enabled ran while the service lock was held: taking it from inside Enabled blocked")
	}
}

// A nudge queued for a usage-limited session starts failover; once the session
// is on another account the pause ends and the nudge goes out before the reset.
func TestUsageLimitFailoverDrainsQueuedAutomationOnNewAccount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	if err := h.st.BindSessionAgentAccount(ctx, testSession, domain.AgentAccountProviderCodex, "account-a", h.now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	f := &fakeFailover{moveID: "move-1"}
	h.svc.SetUsageLimitFailover(f)

	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "first", ClientMessageID: "c1", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, TurnState: domain.TurnStateFailed,
		Err: errors.New("You've hit your 5-hour limit · resets 2099-01-01T00:00:00Z")})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["first"] == domain.TurnStateFailed
	})
	awaitUsageLimitProjection(t, h.ctrl)
	// The failure arms itself (B319); this test is about the unarmed paused path.
	if err := h.svc.SetTurnAutoResume(ctx, testSession, turn.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "nudge", ClientMessageID: "n1", Origin: domain.MessageOriginAutomation}); err != nil {
		t.Fatal(err)
	}
	// The bare failure moved nothing; only the queued nudge starts a move.
	deadline := time.Now().Add(5 * time.Second)
	started := 0
	for time.Now().Before(deadline) {
		f.mu.Lock()
		started = f.starts
		f.mu.Unlock()
		if started > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if started == 0 || len(h.conv.sentTexts()) != 1 {
		t.Fatalf("starts=%d sent=%v, want a move started and the nudge still queued", started, h.conv.sentTexts())
	}
	h.advance(time.Second)
	if err := h.st.BindSessionAgentAccount(ctx, testSession, domain.AgentAccountProviderCodex, "account-b", h.now()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.finished, f.moved = true, true
	f.mu.Unlock()
	deadline = time.Now().Add(10 * time.Second)
	for len(h.conv.sentTexts()) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := h.conv.sentTexts(); len(got) != 2 || got[1] != "nudge" {
		t.Fatalf("provider received %v, want the queued nudge after the move", got)
	}
}

// A bare failure with nothing queued must not start a move, and an armed prompt
// still retries on the new account when failover was set before the failure.
func TestUsageLimitFailoverBareFailureLeavesArmedRetryToArmedPath(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	if err := h.st.BindSessionAgentAccount(ctx, testSession, domain.AgentAccountProviderCodex, "account-a", h.now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	f := &fakeFailover{moveID: "move-1"}
	h.svc.SetUsageLimitFailover(f)
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	time.Sleep(300 * time.Millisecond)
	f.mu.Lock()
	starts := f.starts
	f.mu.Unlock()
	if starts != 0 {
		t.Fatalf("a bare failure started %d moves, want none", starts)
	}
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.st.BindSessionAgentAccount(ctx, testSession, domain.AgentAccountProviderCodex, "account-b", h.now()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.finished, f.moved = true, true
	f.mu.Unlock()
	for range 2 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(h.conv.sentMessages()); got != 2 {
		t.Fatalf("provider received %d messages, want exactly one retry on the new account", got)
	}
}

// B319: the retry after an account move replays a worker's automation-origin
// prompt on the new account, like the armed reset-time path does.
func TestUsageLimitFailoverReplaysAutomationPromptInWorker(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	restartAsKind(t, h, domain.KindWorker)
	ctx := context.Background()
	turn := failAutomationTurn(t, h)
	if err := h.st.BindSessionAgentAccount(ctx, testSession, domain.AgentAccountProviderCodex, "account-a", h.now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	f := &fakeFailover{moveID: "move-1"}
	h.svc.SetUsageLimitFailover(f)
	for range 2 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.st.BindSessionAgentAccount(ctx, testSession, domain.AgentAccountProviderCodex, "account-b", h.now()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.finished, f.moved = true, true
	f.mu.Unlock()
	for range 2 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := h.conv.sentTexts(); len(got) != 2 || got[1] != "orchestrator nudge" {
		t.Fatalf("sent = %v (turn %s), want the prompt replayed once after the move", got, turn.ID)
	}
}
