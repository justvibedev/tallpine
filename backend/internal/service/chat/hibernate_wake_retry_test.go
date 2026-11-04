package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// sleepingSession hibernates the harness session and returns a second
// service on the same store, standing in for the daemon that wakes it.
func sleepingSession(t *testing.T, kind domain.SessionKind) (*harness, *hibernationConversation, *chatsvc.Service, *fakeConversation) {
	t.Helper()
	h, old := settledHibernationHarness(t, domain.TurnStateCompleted)
	setSessionKind(t, h, kind)
	if hibernated, err := h.svc.HibernateChat(context.Background(), testSession); err != nil || !hibernated {
		t.Fatalf("HibernateChat = %v, %v", hibernated, err)
	}
	resumed := newFakeConversation()
	resumed.turnSeq = 1
	var nextID atomic.Int32
	svc := chatsvc.New(chatsvc.Options{
		Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st,
		Drivers:  fakeRegistry{driver: fakeDriver{conv: resumed}},
		Activity: h.activity, Log: slog.New(slog.DiscardHandler), Now: h.now,
		NewID: func() string { return fmt.Sprintf("wake-retry-%d", nextID.Add(1)) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	return h, old, svc, resumed
}

// wakeAs mimics Session Manager's resume: it clears the sleep marker before
// it launches, then starts the native conversation unless fail says no.
func wakeAs(h *harness, svc *chatsvc.Service, providerID string, attempts *atomic.Int32, fail func(n int32) bool) func(context.Context, domain.SessionID) error {
	return func(ctx context.Context, id domain.SessionID) error {
		n := attempts.Add(1)
		rec, found, err := h.st.GetSession(ctx, id)
		if err != nil || !found {
			return fmt.Errorf("read session: %v %w", found, err)
		}
		if rec.HibernatedAt != nil {
			if _, err := h.st.SetSessionHibernated(ctx, id, rec.Revision, nil); err != nil {
				return err
			}
		}
		if fail(n) {
			return errors.New("agent failed to start")
		}
		_, err = svc.Start(ctx, chatsvc.StartConfig{
			SessionID: id, ProjectID: testProject, Harness: domain.HarnessCodex,
			WorkspacePath: os.TempDir(), ProviderConversationID: providerID,
		})
		return err
	}
}

func waitAttempts(t *testing.T, attempts *atomic.Int32, want int32, retry func()) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for attempts.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("wake attempts = %d, want %d", attempts.Load(), want)
		}
		if retry != nil {
			retry()
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func turnStates(s store.ConversationSnapshot) []domain.TurnState {
	out := make([]domain.TurnState, 0, len(s.Turns))
	for _, turn := range s.Turns {
		out = append(out, turn.State)
	}
	return out
}

func TestFailedWakeKeepsQueuedMessageAndRetriesDelivery(t *testing.T) {
	t.Parallel()

	h, old, svc, resumed := sleepingSession(t, domain.KindWorker)
	ctx := context.Background()
	var attempts atomic.Int32
	svc.SetWakeCallback(wakeAs(h, svc, old.ProviderConversationID(), &attempts, func(n int32) bool { return n == 1 }))

	turn, err := svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "review comments arrived", ClientMessageID: "review-1"})
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send to sleeping agent = %+v, %v", turn, err)
	}
	waitAttempts(t, &attempts, 1, nil)
	time.Sleep(100 * time.Millisecond)
	snapshot, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil || snapshot.Turns[len(snapshot.Turns)-1].State != domain.TurnStateQueued {
		t.Fatalf("message after failed wake: states=%v err=%v; want still queued", turnStates(snapshot), err)
	}
	// Not due yet: nothing retries before the backoff.
	if err := svc.RetryQueuedWakes(ctx); err != nil || attempts.Load() != 1 {
		t.Fatalf("early retry: attempts=%d err=%v", attempts.Load(), err)
	}
	waitAttempts(t, &attempts, 2, func() {
		h.advance(time.Minute)
		_ = svc.RetryQueuedWakes(ctx)
	})
	h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return len(resumed.sentTexts()) == 1 })
	if got := resumed.sentTexts(); !slices.Equal(got, []string{"review comments arrived"}) {
		t.Fatalf("delivered = %v", got)
	}
}

func TestWakeGivesUpAfterRepeatedFailures(t *testing.T) {
	t.Parallel()

	h, old, svc, resumed := sleepingSession(t, domain.KindWorker)
	ctx := context.Background()
	var attempts atomic.Int32
	svc.SetWakeCallback(wakeAs(h, svc, old.ProviderConversationID(), &attempts, func(int32) bool { return true }))
	if _, err := svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "hello", ClientMessageID: "hello-1"}); err != nil {
		t.Fatal(err)
	}
	waitAttempts(t, &attempts, 5, func() {
		h.advance(10 * time.Minute)
		_ = svc.RetryQueuedWakes(ctx)
	})
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Turns[len(s.Turns)-1].State == domain.TurnStateFailed
	})
	h.advance(time.Hour)
	_ = svc.RetryQueuedWakes(ctx)
	time.Sleep(50 * time.Millisecond)
	if attempts.Load() != 5 || len(resumed.sentTexts()) != 0 {
		t.Fatalf("after giving up: attempts=%d sent=%v states=%v", attempts.Load(), resumed.sentTexts(), turnStates(snapshot))
	}
	// The failure is shown as a stopped session with its Resume action, not
	// hidden behind a sleep marker.
	if asleep(t, h) {
		t.Fatal("session still marked asleep after the wake retries were spent")
	}
}

// A daemon that restarts (or quits, or crashes) while a failed wake waits for
// its retry must not strand the queued message: the session stays marked
// asleep, so the next daemon's scan wakes it and delivers the message.
func TestFailedWakeSurvivesDaemonRestartDuringBackoff(t *testing.T) {
	h, old, first, _ := sleepingSession(t, domain.KindWorker)
	ctx := context.Background()
	var firstAttempts atomic.Int32
	first.SetWakeCallback(wakeAs(h, first, old.ProviderConversationID(), &firstAttempts, func(int32) bool { return true }))
	if turn, err := first.Send(ctx, testSession, ports.ChatUserMessage{Text: "CI failed; fix it", ClientMessageID: "ci-1"}); err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send = %+v, %v", turn, err)
	}
	waitAttempts(t, &firstAttempts, 1, nil)
	// The failed resume cleared the marker; the deferred retry sets it again.
	deadline := time.Now().Add(5 * time.Second)
	for !asleep(t, h) {
		if time.Now().After(deadline) {
			t.Fatal("failed wake left the session without a sleep marker")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A fresh daemon on the same store: no in-memory retry schedule.
	resumed := newFakeConversation()
	resumed.turnSeq = 1
	next := chatsvc.New(chatsvc.Options{
		Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st,
		Drivers:  fakeRegistry{driver: fakeDriver{conv: resumed}},
		Activity: h.activity, Log: slog.New(slog.DiscardHandler), Now: h.now,
		NewID: func() string { return "restarted-daemon" },
	})
	t.Cleanup(func() { _ = next.Stop(context.Background(), testSession) })
	var attempts atomic.Int32
	next.SetWakeCallback(wakeAs(h, next, old.ProviderConversationID(), &attempts, func(int32) bool { return false }))
	if err := next.RetryQueuedWakes(ctx); err != nil {
		t.Fatal(err)
	}
	h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return len(resumed.sentTexts()) == 1 })
	if got := resumed.sentTexts(); !slices.Equal(got, []string{"CI failed; fix it"}) || attempts.Load() != 1 {
		t.Fatalf("restarted daemon delivered %v after %d wakes", got, attempts.Load())
	}
	if asleep(t, h) {
		t.Fatal("session still marked asleep after it woke")
	}
}

func TestQueuedMessageOnSleepingSessionWakesAfterDaemonRestart(t *testing.T) {
	t.Parallel()

	h, old, svc, resumed := sleepingSession(t, domain.KindWorker)
	ctx := context.Background()
	// The daemon that accepted the message could not wake the agent (it had
	// no wake path yet, as while it stops); the message and marker persist.
	if turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "queued before restart", ClientMessageID: "restart-1"}); err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send = %+v, %v", turn, err)
	}
	time.Sleep(50 * time.Millisecond)
	rec, _, err := h.st.GetSession(ctx, testSession)
	if err != nil || rec.HibernatedAt == nil {
		t.Fatalf("marker before restart = %v, %v", rec.HibernatedAt, err)
	}
	var attempts atomic.Int32
	svc.SetWakeCallback(wakeAs(h, svc, old.ProviderConversationID(), &attempts, func(int32) bool { return false }))
	if err := svc.RetryQueuedWakes(ctx); err != nil {
		t.Fatal(err)
	}
	h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return len(resumed.sentTexts()) == 1 })
	if got := resumed.sentTexts(); !slices.Equal(got, []string{"queued before restart"}) || attempts.Load() != 1 {
		t.Fatalf("delivered = %v attempts=%d", got, attempts.Load())
	}
}

func TestReportWakesSleepingOrchestrator(t *testing.T) {
	t.Parallel()

	h, old, svc, resumed := sleepingSession(t, domain.KindOrchestrator)
	ctx := context.Background()
	var attempts atomic.Int32
	svc.SetWakeCallback(wakeAs(h, svc, old.ProviderConversationID(), &attempts, func(int32) bool { return false }))
	report := domain.WrapReportDelivery("report-7", "worker mer-2 is done: PR #12 opened")
	if _, err := svc.RelaySessionChatTurn(ctx, testSession, report, "report-7", ports.MessageDeliveryOptions{}); err != nil {
		t.Fatalf("deliver report to sleeping orchestrator: %v", err)
	}
	h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return len(resumed.sentTexts()) == 1 })
	if got := resumed.sentTexts(); len(got) != 1 || got[0] != report {
		t.Fatalf("orchestrator received %v", got)
	}
	if rec, _, err := h.st.GetSession(ctx, testSession); err != nil || rec.HibernatedAt != nil || rec.Kind != domain.KindOrchestrator {
		t.Fatalf("orchestrator after report: marker=%v kind=%s err=%v", rec.HibernatedAt, rec.Kind, err)
	}
}

// asResumeFailure maps a wake error onto the error a real failed native resume
// returns (ACP session/load timing out after a restart).
func asResumeFailure(wake func(context.Context, domain.SessionID) error) func(context.Context, domain.SessionID) error {
	return func(ctx context.Context, id domain.SessionID) error {
		if err := wake(ctx, id); err != nil {
			return fmt.Errorf("%w: ACP session/load: %w", ports.ErrChatResumeFailed, err)
		}
		return nil
	}
}

// After a restart the first native resume can fail while the provider is
// still settling. An open view must not leave the session stopped until a
// person presses Resume: the wake is retried on its schedule and recovers.
func TestFailedViewWakeIsRetriedAndRecovers(t *testing.T) {
	t.Parallel()

	h, old, svc, _ := sleepingSession(t, domain.KindOrchestrator)
	ctx := context.Background()
	var attempts atomic.Int32
	svc.SetWakeCallback(asResumeFailure(wakeAs(h, svc, old.ProviderConversationID(), &attempts, func(n int32) bool { return n == 1 })))

	if err := svc.SetChatView(ctx, testSession, "phone", true); !errors.Is(err, ports.ErrChatResumeFailed) {
		t.Fatalf("view wake = %v, want the resume failure", err)
	}
	if svc.HasLiveChatController(testSession) {
		t.Fatal("controller live after a failed wake")
	}
	waitAttempts(t, &attempts, 2, func() {
		// Within the lease, so each heartbeat renews the view rather than
		// opening a new one (which would wake on its own).
		h.advance(25 * time.Second)
		_ = svc.SetChatView(ctx, testSession, "phone", true)
		_ = svc.RetryQueuedWakes(ctx)
	})
	deadline := time.Now().Add(5 * time.Second)
	for !svc.HasLiveChatController(testSession) {
		if time.Now().After(deadline) {
			t.Fatalf("no live controller after retried wake; attempts=%d", attempts.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A send that arrives while a wake is starting joins that wake. When the wake
// fails, the message is queued for the retried wake and delivered once,
// rather than failing with CHAT_RESUME_FAILED.
func TestSendDuringFailingWakeQueuesAndDeliversOnce(t *testing.T) {
	t.Parallel()

	h, old, svc, resumed := sleepingSession(t, domain.KindWorker)
	ctx := context.Background()
	var attempts atomic.Int32
	inWake, release := make(chan struct{}), make(chan struct{})
	svc.SetWakeCallback(asResumeFailure(wakeAs(h, svc, old.ProviderConversationID(), &attempts, func(n int32) bool {
		if n != 1 {
			return false
		}
		close(inWake)
		<-release
		return true
	})))

	viewDone := make(chan error, 1)
	go func() { viewDone <- svc.SetChatView(ctx, testSession, "phone", true) }()
	<-inWake // the wake has cleared the sleep marker and is starting
	type result struct {
		turn domain.ConversationTurn
		err  error
	}
	second := make(chan result, 1)
	go func() {
		turn, err := svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "while starting", ClientMessageID: "m-1"})
		second <- result{turn, err}
	}()
	time.Sleep(100 * time.Millisecond) // let the send join the running wake
	close(release)
	got := <-second
	if got.err != nil || got.turn.State != domain.TurnStateQueued {
		t.Fatalf("send during failing wake = %+v, %v; want queued", got.turn, got.err)
	}
	// Delivered by the wake the queued send kicks, not the retry timer: no
	// clock advance and no retry scan before delivery.
	<-viewDone
	h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return len(resumed.sentTexts()) == 1 })
	for range 3 {
		h.advance(time.Hour)
		_ = svc.RetryQueuedWakes(ctx)
	}
	time.Sleep(50 * time.Millisecond)
	if sent := resumed.sentTexts(); !slices.Equal(sent, []string{"while starting"}) {
		t.Fatalf("delivered = %v, want the message exactly once", sent)
	}
}

// A provider start can hang until its handshake timeout. Opening a view must
// return well before a phone client abandons the request (about 12 s); the
// wake keeps going and its result arrives as session events.
func TestOpeningViewDoesNotWaitForASlowWake(t *testing.T) {
	restore := chatsvc.SetChatViewWakeWait(50 * time.Millisecond)
	defer restore()

	h, old, svc, _ := sleepingSession(t, domain.KindOrchestrator)
	ctx := context.Background()
	var attempts atomic.Int32
	release := make(chan struct{})
	svc.SetWakeCallback(wakeAs(h, svc, old.ProviderConversationID(), &attempts, func(int32) bool {
		<-release
		return false
	}))
	for _, viewer := range []string{"phone", "desktop"} {
		returned := make(chan error, 1)
		go func() { returned <- svc.SetChatView(ctx, testSession, viewer, true) }()
		select {
		case err := <-returned:
			if err != nil {
				t.Fatalf("%s view during slow wake = %v", viewer, err)
			}
		case <-time.After(2 * time.Second):
			close(release)
			t.Fatalf("%s view blocked on a slow wake", viewer)
		}
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for !svc.HasLiveChatController(testSession) {
		if time.Now().After(deadline) {
			t.Fatal("wake did not finish after the view returned")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if attempts.Load() != 1 {
		t.Fatalf("wake attempts = %d, want the viewers to share one", attempts.Load())
	}
}

// A concurrent wake can start the provider and finish its drain between the
// failed send reading the session and queueing the message. The message must
// still be delivered, exactly once.
func TestSendAfterFailedWakeRacingASuccessfulWakeDeliversOnce(t *testing.T) {
	h, old, svc, resumed := sleepingSession(t, domain.KindWorker)
	ctx := context.Background()
	var attempts atomic.Int32
	inWake, release := make(chan struct{}), make(chan struct{})
	svc.SetWakeCallback(asResumeFailure(wakeAs(h, svc, old.ProviderConversationID(), &attempts, func(n int32) bool {
		if n != 1 {
			return false
		}
		close(inWake)
		<-release
		return true
	})))
	restore := chatsvc.SetAfterFailedWakeRead(func() {
		// The racing wake: another viewer opens, wakes, and drains (nothing yet).
		if err := svc.SetChatView(ctx, testSession, "desktop", true); err != nil {
			t.Errorf("racing wake = %v", err)
		}
	})
	defer restore()

	viewDone := make(chan error, 1)
	go func() { viewDone <- svc.SetChatView(ctx, testSession, "phone", true) }()
	<-inWake
	sent := make(chan error, 1)
	go func() {
		_, err := svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "raced", ClientMessageID: "r-1"})
		sent <- err
	}()
	time.Sleep(100 * time.Millisecond)
	close(release)
	if err := <-sent; err != nil {
		t.Fatalf("send = %v", err)
	}
	<-viewDone
	h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return len(resumed.sentTexts()) == 1 })
	time.Sleep(100 * time.Millisecond)
	if got := resumed.sentTexts(); !slices.Equal(got, []string{"raced"}) {
		t.Fatalf("delivered = %v, want exactly once", got)
	}
}

// One failed wake is one attempt, however many callers joined it: a view and
// the queued message's background wake both see the same failure.
func TestSharedFailedWakeSpendsOneAttempt(t *testing.T) {
	t.Parallel()

	h, old, svc, _ := sleepingSession(t, domain.KindWorker)
	ctx := context.Background()
	var attempts atomic.Int32
	inWake, release := make(chan struct{}), make(chan struct{})
	failing := asResumeFailure(wakeAs(h, svc, old.ProviderConversationID(), &attempts, func(int32) bool { return true }))
	var calls atomic.Int32
	svc.SetWakeCallback(func(ctx context.Context, id domain.SessionID) error {
		if calls.Add(1) == 1 {
			// Hold the first wake before it clears the sleep marker, so the
			// view below joins this same wake rather than finding no marker.
			close(inWake)
			<-release
		}
		return failing(ctx, id)
	})
	if _, err := svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "hello", ClientMessageID: "h-1"}); err != nil {
		t.Fatal(err)
	}
	<-inWake
	viewDone := make(chan error, 1)
	go func() { viewDone <- svc.SetChatView(ctx, testSession, "phone", true) }()
	time.Sleep(100 * time.Millisecond) // the view joins the running wake
	close(release)
	<-viewDone
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		if s.Turns[len(s.Turns)-1].State == domain.TurnStateFailed {
			return true
		}
		h.advance(10 * time.Minute)
		_ = svc.RetryQueuedWakes(ctx)
		return false
	})
	time.Sleep(50 * time.Millisecond)
	if got := attempts.Load(); got != 5 {
		t.Fatalf("wake attempts before giving up = %d, want 5; states=%v", got, turnStates(snapshot))
	}
}

// Every joiner of one failed wake (the view goroutine, the queued send, the
// background drain) records the same failure. Concurrent joiners must advance
// the attempt counter and backoff exactly once, or the five-attempt budget
// shrinks to two or three real attempts.
func TestConcurrentJoinersOfOneFailedWakeAdvanceRetryOnce(t *testing.T) {
	t.Parallel()

	h, _, svc, _ := sleepingSession(t, domain.KindWorker)
	const joiners = 8
	for round := 1; round <= 4; round++ {
		var wg sync.WaitGroup
		gate := make(chan struct{})
		for i := 0; i < joiners; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-gate
				if !svc.DeferWake(testSession) {
					t.Errorf("round %d: DeferWake reported attempts spent", round)
				}
			}()
		}
		close(gate)
		wg.Wait()
		failures, next, pending := svc.WakeRetryState(testSession)
		if !pending || failures != round {
			t.Fatalf("round %d: failures = %d (pending %v), want %d: %d joiners counted one failure", round, failures, pending, round, joiners)
		}
		if want := h.now().Add(30 * time.Second << (round - 1)); !next.Equal(want) {
			t.Fatalf("round %d: next = %v, want %v (backoff advanced more than once)", round, next, want)
		}
		h.advance(10 * time.Minute) // the retry comes due
	}
	// The fifth failed wake spends the last attempt, however many joined it.
	if svc.DeferWake(testSession) {
		t.Fatal("fifth failed wake still scheduled a retry; the budget was not exactly five")
	}
}
