package chat_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func failUsageLimitedTurn(t *testing.T, h *harness, message string) domain.ConversationTurn {
	t.Helper()
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "finish this task", ClientMessageID: uuid.NewString(), Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(message)})
	snapshot := failedTurnSnapshot(t, h, turn.ID)
	awaitUsageLimitProjection(t, h.ctrl)
	failed, _ := turnByID(snapshot, turn.ID)
	return failed
}

// failAutoResumeTurn fails a turn on a timed usage limit and opts it in to
// auto-resume.
func failAutoResumeTurn(ctx context.Context, t *testing.T, h *harness) domain.ConversationTurn {
	t.Helper()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	return source
}

// failAutoResumeTurnOnAccountA is failAutoResumeTurn for a session bound to
// "account-a" a minute ago.
func failAutoResumeTurnOnAccountA(ctx context.Context, t *testing.T, h *harness) domain.ConversationTurn {
	t.Helper()
	source := failAutoResumeTurn(ctx, t, h)
	if err := h.st.BindSessionAgentAccount(ctx, testSession, domain.AgentAccountProviderCodex, "account-a", h.now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	return source
}

func awaitUsageLimitProjection(t *testing.T, controller *chatsvc.Controller) {
	t.Helper()
	// A failed row commits before lifecycle cleanup releases sendMu. Scheduled
	// retries deliberately use TryLock, so the row alone is not a dispatch barrier.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := controller.WaitProjection(ctx); err != nil {
		t.Fatalf("wait for usage-limit completion: %v", err)
	}
}

const timedUsageLimit = "You've hit your session limit · resets 3:31pm (Asia/Calcutta)"

var errUsageLimitTemporaryRead = errors.New("database is temporarily unavailable")

type usageLimitReadStore struct {
	*sqlite.Store
	reads         atomic.Int32
	failTurn      atomic.Bool
	failPrompt    atomic.Bool
	failSession   atomic.Bool
	attemptBudget atomic.Int64
	afterLatest   func()
}

func (s *usageLimitReadStore) TurnByID(ctx context.Context, id string) (domain.ConversationTurn, error) {
	s.reads.Add(1)
	if s.failTurn.Swap(false) {
		return domain.ConversationTurn{}, errUsageLimitTemporaryRead
	}
	return s.Store.TurnByID(ctx, id)
}

func (s *usageLimitReadStore) RetryPrompt(ctx context.Context, conversationID, id string) (domain.RetryPrompt, error) {
	s.reads.Add(1)
	if s.failPrompt.Swap(false) {
		return domain.RetryPrompt{}, errUsageLimitTemporaryRead
	}
	return s.Store.RetryPrompt(ctx, conversationID, id)
}

func (s *usageLimitReadStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	s.reads.Add(1)
	if s.failSession.Swap(false) {
		return domain.SessionRecord{}, false, errUsageLimitTemporaryRead
	}
	if deadline, ok := ctx.Deadline(); ok {
		s.attemptBudget.Store(int64(time.Until(deadline)))
	}
	return s.Store.GetSession(ctx, id)
}

func (s *usageLimitReadStore) LatestAutoResumeTurn(ctx context.Context, conversationID string) (domain.ConversationTurn, error) {
	s.reads.Add(1)
	turn, err := s.Store.LatestAutoResumeTurn(ctx, conversationID)
	if err == nil && s.afterLatest != nil {
		hook := s.afterLatest
		s.afterLatest = nil
		hook()
	}
	return turn, err
}

func newUsageLimitReadHarness(t *testing.T) (*harness, *usageLimitReadStore) {
	t.Helper()
	h := newHarness(t)
	ctx := context.Background()
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	wrapped := &usageLimitReadStore{Store: h.st}
	provider := newFakeConversation()
	svc := chatsvc.New(chatsvc.Options{
		Store: wrapped, Reader: fullSnapshotReader(h.st), PageReader: boundedSnapshotReader(h.st), Sessions: wrapped,
		Drivers: fakeRegistry{driver: fakeDriver{conv: provider}}, NewID: uuid.NewString, Now: h.now,
	})
	controller, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
	h.svc, h.ctrl, h.conv = svc, controller, provider
	return h, wrapped
}

func TestUsageLimitFutureResetSkipsEligibilityReads(t *testing.T) {
	t.Parallel()

	h, wrapped := newUsageLimitReadHarness(t)
	ctx := context.Background()
	failAutoResumeTurn(ctx, t, h)
	reads := wrapped.reads.Load()
	for range 5 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := wrapped.reads.Load(); got != reads {
		t.Fatalf("future sweep did %d eligibility reads", got-reads)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if wrapped.reads.Load() == reads || len(h.conv.sentMessages()) != 2 {
		t.Fatal("deadline did not trigger validation and one retry")
	}
	if budget := time.Duration(wrapped.attemptBudget.Load()); budget < time.Minute || budget > 90*time.Second {
		t.Fatalf("automatic attempt budget = %s, want room for a 60-second native wake", budget)
	}
}

func TestUsageLimitTransientReadsBackOffAndRecover(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{"turn", "prompt", "session"} {
		t.Run(stage, func(t *testing.T) {
			h, wrapped := newUsageLimitReadHarness(t)
			ctx := context.Background()
			source := failAutoResumeTurn(ctx, t, h)
			h.advance(time.Minute)
			switch stage {
			case "turn":
				wrapped.failTurn.Store(true)
			case "prompt":
				wrapped.failPrompt.Store(true)
			case "session":
				wrapped.failSession.Store(true)
			}
			if err := h.svc.ResumeUsageLimitedTurns(ctx); !errors.Is(err, errUsageLimitTemporaryRead) {
				t.Fatalf("transient sweep = %v", err)
			}
			after, err := h.st.TurnByID(ctx, source.ID)
			if err != nil || !after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" {
				t.Fatalf("temporary read failure consumed intent: %+v, %v", after.UsageLimit, err)
			}
			reads := wrapped.reads.Load()
			h.advance(29 * time.Second)
			for range 3 {
				if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if wrapped.reads.Load() != reads || len(h.conv.sentMessages()) != 1 {
				t.Fatal("temporary failure was retried during backoff")
			}
			h.advance(time.Second)
			if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
				t.Fatal(err)
			}
			after, err = h.st.TurnByID(ctx, source.ID)
			if err != nil || after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" || len(h.conv.sentMessages()) != 2 {
				t.Fatalf("recovered sweep did not dispatch once: %+v, %v", after.UsageLimit, err)
			}
		})
	}
}

func TestUsageLimitManualRetryDuringSweepKeepsConsumedSourceClean(t *testing.T) {
	t.Parallel()

	h, wrapped := newUsageLimitReadHarness(t)
	ctx := context.Background()
	source := failAutoResumeTurn(ctx, t, h)
	wrapped.afterLatest = func() {
		if _, err := h.svc.RetryTurn(ctx, testSession, source.ID); err != nil {
			t.Fatalf("manual retry: %v", err)
		}
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" || len(h.conv.sentMessages()) != 2 {
		t.Fatalf("sweep overwrote manually consumed intent: %+v, %v", after.UsageLimit, err)
	}
}

func TestUsageLimitAutoResumeRecoversAfterControllerHandoff(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	source := failAutoResumeTurn(ctx, t, h)
	if err := h.ctrl.BeginIdleBranchHandoff(ctx); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); !errors.Is(err, chatsvc.ErrControllerHandoff) {
		t.Fatalf("handoff sweep = %v", err)
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || !after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" {
		t.Fatalf("handoff consumed intent: %+v, %v", after.UsageLimit, err)
	}
	h.ctrl.AbortHandoff()
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.conv.sentMessages()) != 1 {
		t.Fatal("handoff recovery ignored backoff")
	}
	h.advance(30 * time.Second)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.conv.sentMessages()) != 2 {
		t.Fatal("handoff recovery did not resume the armed prompt")
	}
}

type cancelledUsageLimitScanStore struct {
	*sqlite.Store
	afterScan func()
}

func (s *cancelledUsageLimitScanStore) AutoResumeTurns(ctx context.Context) ([]domain.ConversationTurn, error) {
	turns, err := s.Store.AutoResumeTurns(ctx)
	if err == nil && s.afterScan != nil {
		s.afterScan()
	}
	return turns, err
}

func TestUsageLimitCancellationAfterSweepScanPreventsDispatch(t *testing.T) {
	t.Parallel()

	var wrapped *cancelledUsageLimitScanStore
	h := newHarnessWithConversationAndStore(t, nil, func(st *sqlite.Store) chatsvc.Store {
		wrapped = &cancelledUsageLimitScanStore{Store: st}
		return wrapped
	})
	ctx := context.Background()
	source := failAutoResumeTurn(ctx, t, h)
	wrapped.afterScan = func() {
		if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.conv.sentMessages()) != 1 {
		t.Fatal("sweep used a stale consent snapshot after cancellation")
	}
}

func TestUsageLimitAutoResumeWaitsAndDispatchesOnce(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if source.UsageLimit == nil || source.UsageLimit.ResetsAt == nil || !source.UsageLimit.ResetsAt.Equal(h.now().Add(time.Minute)) {
		t.Fatalf("historical limit = %+v, want an absolute reset in one minute", source.UsageLimit)
	}
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.conv.sentMessages()) != 1 {
		t.Fatal("auto-resume sent before reset")
	}
	h.advance(time.Minute)
	for range 2 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.conv.sentMessages()) != 2 {
		t.Fatalf("provider received %d messages, want one retry", len(h.conv.sentMessages()))
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || after.UsageLimit == nil || after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" {
		t.Fatalf("source = %+v, %v, want consumed consent", after, err)
	}
}

func TestUsageLimitAutoResumeCancellationWorksWhileBusy(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	source := failAutoResumeTurn(ctx, t, h)
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "new work", ClientMessageID: "new", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, false); err != nil {
		t.Fatalf("cancel while busy: %v", err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.conv.sentMessages()) != 2 {
		t.Fatal("cancelled source was dispatched")
	}
}

func TestUsageLimitAutoResumeStopsAfterNewerCompletedTurn(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	source := failAutoResumeTurn(ctx, t, h)
	newer, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "continue differently", ClientMessageID: "new", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: newer.ProviderTurnID, TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		turn, found := turnByID(s, newer.ID)
		return found && turn.State == domain.TurnStateCompleted
	})
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); !errors.Is(err, chatsvc.ErrUsageLimitAutoResumeInvalid) {
		t.Fatalf("enable superseded source = %v", err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || after.UsageLimit.AutoResume || !strings.Contains(after.UsageLimit.ResumeError, "newer turn") {
		t.Fatalf("source limit = %+v, %v", after.UsageLimit, err)
	}
	if len(h.conv.sentMessages()) != 2 {
		t.Fatal("superseded source was retried")
	}
}

func TestUsageLimitAutoResumeDurableAcrossServiceRestart(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	failAutoResumeTurn(ctx, t, h)
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
	provider.turnSeq = 1 // Native resume preserves the provider's existing turn IDs.
	restarted := chatsvc.New(chatsvc.Options{
		Store: reopened, Reader: fullSnapshotReader(reopened), Sessions: reopened,
		Drivers: fakeRegistry{driver: fakeDriver{conv: provider}}, NewID: uuid.NewString, Now: h.now,
	})
	if _, err := restarted.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Stop(ctx, testSession) })
	if err := restarted.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(provider.sentMessages()) != 0 {
		t.Fatal("restart advanced the reset deadline")
	}
	h.advance(time.Minute)
	if err := restarted.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if len(provider.sentMessages()) != 1 || provider.sentMessages()[0].Text != "finish this task" {
		t.Fatalf("resumed sends = %+v", provider.sentMessages())
	}
}

func TestUsageLimitAutoResumeWakesHibernatedChatWithoutAView(t *testing.T) {
	t.Parallel()

	h, old := settledHibernationHarness(t, "")
	ctx := context.Background()
	source := failAutoResumeTurn(ctx, t, h)
	if slept, err := h.svc.HibernateChat(ctx, testSession); err != nil || !slept {
		t.Fatalf("hibernate = %v, %v", slept, err)
	}
	resumed := newFakeConversation()
	resumed.turnSeq = 1
	wakeService := chatsvc.New(chatsvc.Options{
		Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: resumed}}, NewID: uuid.NewString, Now: h.now,
	})
	t.Cleanup(func() { _ = wakeService.Stop(ctx, testSession) })
	wakeCalls := 0
	wakeService.SetWakeCallback(func(ctx context.Context, id domain.SessionID) error {
		wakeCalls++
		record, _, err := h.st.GetSession(ctx, id)
		if err != nil {
			return err
		}
		if applied, err := h.st.SetSessionHibernated(ctx, id, record.Revision, nil); err != nil || !applied {
			return errors.New("could not clear hibernation marker")
		}
		_, err = wakeService.Start(ctx, chatsvc.StartConfig{SessionID: id, ProjectID: testProject,
			Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), ProviderConversationID: old.ProviderConversationID()})
		return err
	})
	h.advance(30 * time.Second)
	if err := wakeService.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if wakeCalls != 1 || len(resumed.sentMessages()) != 1 {
		t.Fatalf("wake calls = %d, sends = %+v", wakeCalls, resumed.sentMessages())
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" {
		t.Fatalf("source = %+v, %v", after.UsageLimit, err)
	}
}

func TestUsageLimitDaemonShutdownKeepsUnconsumedIntent(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	ctx := context.Background()
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := h.svc.ResumeUsageLimitedTurns(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown sweep = %v", err)
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || !after.UsageLimit.AutoResume {
		t.Fatalf("shutdown lost consent: %+v, %v", after.UsageLimit, err)
	}
}

func TestUsageLimitAutoResumeStopsAfterRollback(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	source := failAutoResumeTurn(ctx, t, h)
	if _, err := h.st.RollbackTurns(ctx, source.ConversationID, source.ID, h.now()); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || after.UsageLimit.AutoResume || after.UsageLimit.ResumeError == "" || len(h.conv.sentMessages()) != 1 {
		t.Fatalf("rolled-back source = %+v, %v", after.UsageLimit, err)
	}
}

func TestUsageLimitAutoResumeDefersBusyCompaction(t *testing.T) {
	t.Parallel()

	provider := newCompactingConversation()
	h := newHarnessWithConversation(t, provider)
	h.conv = provider.fakeConversation
	ctx := context.Background()
	source := failAutoResumeTurn(ctx, t, h)
	if _, err := h.svc.Compact(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); !errors.Is(err, chatsvc.ErrTurnRunning) {
		t.Fatalf("busy sweep = %v", err)
	}
	if len(provider.sentMessages()) != 1 {
		t.Fatal("auto-resume dispatched during compaction")
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || !after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" {
		t.Fatalf("busy source = %+v, %v", after.UsageLimit, err)
	}
}

type waitingUsageLimitCompaction struct {
	*compactingConversation
	started chan struct{}
	release chan struct{}
}

func (c *waitingUsageLimitCompaction) Compact(context.Context) (ports.ChatCompactionResult, error) {
	close(c.started)
	<-c.release
	return ports.ChatCompactionResult{}, nil
}

func TestUsageLimitAutoResumeDoesNotWaitForStuckDispatchLock(t *testing.T) {
	t.Parallel()

	provider := &waitingUsageLimitCompaction{compactingConversation: newCompactingConversation(), started: make(chan struct{}), release: make(chan struct{})}
	h := newHarnessWithConversation(t, provider)
	h.conv = provider.fakeConversation
	ctx := context.Background()
	source := failAutoResumeTurn(ctx, t, h)
	compacted := make(chan struct{})
	go func() { _, _ = h.svc.Compact(ctx, testSession); close(compacted) }()
	<-provider.started
	defer func() { close(provider.release); <-compacted }()
	h.advance(time.Minute)
	swept := make(chan error, 1)
	go func() { swept <- h.svc.ResumeUsageLimitedTurns(ctx) }()
	select {
	case err := <-swept:
		if !errors.Is(err, chatsvc.ErrTurnRunning) {
			t.Fatalf("locked sweep = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("automatic retry blocked behind provider I/O")
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || !after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" || len(provider.sentMessages()) != 1 {
		t.Fatalf("stuck provider source = %+v, %v", after.UsageLimit, err)
	}
}

func TestUsageLimitAutoResumeStopsWhenSessionTerminated(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	source := failAutoResumeTurn(ctx, t, h)
	record, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	record.IsTerminated = true
	if err := h.st.UpdateSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || after.UsageLimit.AutoResume || after.UsageLimit.ResumeError == "" {
		t.Fatalf("terminated source = %+v, %v", after.UsageLimit, err)
	}
	if len(h.conv.sentMessages()) != 1 {
		t.Fatal("auto-resume sent to terminated session")
	}
}

func TestUsageLimitAutoResumeUnknownResetRefusesConsent(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	source := failUsageLimitedTurn(t, h, "Usage limit reached. Resets tomorrow.")
	if err := h.svc.SetTurnAutoResume(context.Background(), testSession, source.ID, true); !errors.Is(err, chatsvc.ErrUsageLimitResetUnknown) {
		t.Fatalf("enable = %v, want unknown reset", err)
	}
}

func TestUsageLimitDispatchFailureConsumesSourceWithoutRepeating(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	source := failAutoResumeTurn(ctx, t, h)
	h.conv.mu.Lock()
	h.conv.sendErr = errors.New("provider disconnected")
	h.conv.mu.Unlock()
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err == nil || !strings.Contains(err.Error(), "provider disconnected") {
		t.Fatalf("dispatch failure = %v", err)
	}
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || after.UsageLimit.AutoResume || after.UsageLimit.ResumeError != "" {
		t.Fatalf("source limit = %+v, %v", after.UsageLimit, err)
	}
	retryID, found, err := h.st.RetryTurnIDForSource(ctx, source.ConversationID, source.ID)
	if err != nil || !found {
		t.Fatalf("failed attempt = %q, %v, %v", retryID, found, err)
	}
	retry, err := h.st.TurnByID(ctx, retryID)
	if err != nil || retry.State != domain.TurnStateFailed || !strings.Contains(retry.ErrorMessage, "provider disconnected") {
		t.Fatalf("failed attempt lost its outcome: %+v, %v", retry, err)
	}
	h.conv.mu.Lock()
	calls := h.conv.sendCalls
	h.conv.mu.Unlock()
	if calls != 2 {
		t.Fatalf("provider sends = %d, want original plus one failed attempt", calls)
	}
}

func TestUsageLimitManualAndScheduledRetryCannotDuplicate(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	source := failAutoResumeTurn(ctx, t, h)
	h.advance(time.Minute)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = h.svc.RetryTurn(ctx, testSession, source.ID) }()
	go func() { defer wg.Done(); _ = h.svc.ResumeUsageLimitedTurns(ctx) }()
	wg.Wait()
	if len(h.conv.sentMessages()) != 2 {
		t.Fatalf("provider sends = %d, want original plus one retry", len(h.conv.sentMessages()))
	}
}

// B319: nobody clicks "resume" for an unattended worker, so a timed limit
// arms its own auto-resume and the sweep retries right after the reset.
func restartAsKind(t *testing.T, h *harness, kind domain.SessionKind) {
	t.Helper()
	ctx := context.Background()
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	provider := newFakeConversation()
	svc := chatsvc.New(chatsvc.Options{
		Store: h.st, Reader: fullSnapshotReader(h.st), PageReader: boundedSnapshotReader(h.st), Sessions: h.st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: provider}}, NewID: uuid.NewString, Now: h.now,
	})
	controller, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject, Kind: kind,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
	h.svc, h.ctrl, h.conv = svc, controller, provider
}

func TestUsageLimitTimedFailureArmsAutoResumeWithoutConsent(t *testing.T) {
	t.Parallel()

	for _, kind := range []domain.SessionKind{domain.KindWorker, domain.KindOrchestrator} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			restartAsKind(t, h, kind)
			assertTimedFailureArms(t, h)
		})
	}
}

// An interactive chat (no worker/orchestrator kind) keeps the consent button.
func TestUsageLimitInteractiveChatIsNotArmedAutomatically(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	got, err := h.st.TurnByID(context.Background(), source.ID)
	if err != nil || got.UsageLimit == nil || got.UsageLimit.AutoResume {
		t.Fatalf("limit = %+v, %v, want unarmed", got.UsageLimit, err)
	}
}

func assertTimedFailureArms(t *testing.T, h *harness) {
	t.Helper()
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	// The snapshot predates the post-commit arming; read the row after it.
	armed, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || armed.UsageLimit == nil || !armed.UsageLimit.AutoResume {
		t.Fatalf("limit = %+v, %v, want auto-resume armed", armed.UsageLimit, err)
	}
	h.advance(time.Minute)
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(h.conv.sentMessages()); got != 2 {
		t.Fatalf("provider received %d messages, want the retry", got)
	}
}

// B319: a history replay re-settles the failed turn with no cause; the limit
// text (and its reset time) must survive.
func TestUsageLimitErrorSurvivesReplaySettle(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurn(t, h, timedUsageLimit)
	if err := h.st.SettleTurn(ctx, source.ConversationID, source.ProviderTurnID, domain.TurnStateFailed, "", h.now()); err != nil {
		t.Fatal(err)
	}
	after, err := h.st.TurnByID(ctx, source.ID)
	if err != nil || after.ErrorMessage != timedUsageLimit {
		t.Fatalf("error = %q, %v, want it preserved", after.ErrorMessage, err)
	}
}

func failAutomationTurn(t *testing.T, h *harness) domain.ConversationTurn {
	t.Helper()
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "orchestrator nudge", ClientMessageID: uuid.NewString(), Origin: domain.MessageOriginAutomation,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
	failedTurnSnapshot(t, h, turn.ID)
	awaitUsageLimitProjection(t, h.ctrl)
	return turn
}

// B319: after the opening brief every worker turn is automation-origin
// (orchestrator sends, relayed reports); those must resume too.
func TestUsageLimitAutomationTurnInWorkerResumesAfterReset(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	restartAsKind(t, h, domain.KindWorker)
	ctx := context.Background()
	turn := failAutomationTurn(t, h)
	armed, err := h.st.TurnByID(ctx, turn.ID)
	if err != nil || armed.UsageLimit == nil || !armed.UsageLimit.AutoResume {
		t.Fatalf("limit = %+v, %v, want armed", armed.UsageLimit, err)
	}
	h.advance(time.Minute)
	for range 2 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := h.conv.sentTexts(); len(got) != 2 || got[1] != "orchestrator nudge" {
		t.Fatalf("sent = %v, want the prompt replayed exactly once", got)
	}
}

// An interactive chat never replays automation prompts.
func TestUsageLimitAutomationTurnInInteractiveChatIsNotArmed(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	turn := failAutomationTurn(t, h)
	got, err := h.st.TurnByID(context.Background(), turn.ID)
	if err != nil || got.UsageLimit == nil || got.UsageLimit.AutoResume {
		t.Fatalf("limit = %+v, %v, want unarmed", got.UsageLimit, err)
	}
}

// B317: the durable turn-ended notice of a limited turn carries the pause.
func TestUsageLimitedTurnRecordsPauseOnItsFallback(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	restartAsKind(t, h, domain.KindWorker)
	// The harness session row is an orchestrator; fallbacks exist for workers.
	if _, err := fallbackTestDB(t, h.st).Exec("UPDATE sessions SET kind = 'worker'"); err != nil {
		t.Fatal(err)
	}
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "work", ClientMessageID: uuid.NewString(), Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID, ProviderEventID: "ev-start"})
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		TurnState: domain.TurnStateFailed, ProviderEventID: "ev-end", Err: errors.New(timedUsageLimit)})
	failedTurnSnapshot(t, h, turn.ID)
	awaitUsageLimitProjection(t, h.ctrl)
	pending, err := h.st.ListPendingTurnFallbacks(context.Background())
	if err != nil || len(pending) != 1 {
		t.Fatalf("fallbacks = %+v, %v", pending, err)
	}
	until, auto, ok := domain.ParseUsageLimitFallbackExcerpt(pending[0].Excerpt)
	if !ok || !auto || !until.Equal(h.now().Add(time.Minute)) {
		t.Fatalf("excerpt = %q, want the reset as pause end", pending[0].Excerpt)
	}
}

// A limit with no usable reset time cannot be armed, so the notice must not promise a resume.
func TestUsageLimitWithoutResetTimeRecordsPauseWithoutAutoResume(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	restartAsKind(t, h, domain.KindWorker)
	// The harness session row is an orchestrator; fallbacks exist for workers.
	if _, err := fallbackTestDB(t, h.st).Exec("UPDATE sessions SET kind = 'worker'"); err != nil {
		t.Fatal(err)
	}
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "work", ClientMessageID: uuid.NewString(), Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID, ProviderEventID: "ev-start"})
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		TurnState: domain.TurnStateFailed, ProviderEventID: "ev-end", Err: errors.New("You've hit your 5-hour limit · resets 2:20am")})
	failedTurnSnapshot(t, h, turn.ID)
	awaitUsageLimitProjection(t, h.ctrl)
	pending, err := h.st.ListPendingTurnFallbacks(context.Background())
	if err != nil || len(pending) != 1 {
		t.Fatalf("fallbacks = %+v, %v", pending, err)
	}
	until, auto, ok := domain.ParseUsageLimitFallbackExcerpt(pending[0].Excerpt)
	if !ok || auto || !until.Equal(h.now().Add(domain.UsageLimitFallbackPause)) {
		t.Fatalf("excerpt = %q, want the fallback pause and no auto-resume", pending[0].Excerpt)
	}
}
