package lifecycle

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// sendMuMessenger models chat Controller.Send, which takes the controller's
// sendMu — the lock the chat event loop holds while it projects TurnCompleted
// and reports the idle activity signal to lifecycle.
type sendMuMessenger struct {
	mu      *sync.Mutex
	entered chan struct{}
	once    sync.Once
	msgs    []string // written under mu
}

func (s *sendMuMessenger) Send(_ context.Context, _ domain.SessionID, msg string) error {
	s.once.Do(func() { close(s.entered) })
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, msg)
	return nil
}

// A CI nudge racing a Chat turn completion: sendOnce holds react.mu while it
// waits for sendMu, and the event loop holds sendMu while it reports the idle
// signal. The turn-end hook must never wait on react.mu, or both block forever
// (and with them every lifecycle reaction, since react.mu is global).
func TestTurnEndHookDoesNotDeadlockWithAReactionSend(t *testing.T) {
	st := newFakeStore()
	var sendMu sync.Mutex
	msgr := &sendMuMessenger{mu: &sendMu, entered: make(chan struct{})}
	m := New(st, msgr)
	st.sessions["mer-1"] = working("mer-1") // mid-turn
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", SessionID: "mer-1", Number: 7, Provider: "github", Host: "github.com", Repo: "o/r"}}

	// Chat event loop: TurnCompleted is being projected under sendMu. Released
	// exactly once, by the hook goroutine or by cleanup on a failing run, so a
	// deadlocked run unwinds instead of leaking both goroutines.
	sendMu.Lock()
	releaseSendMu := sync.OnceFunc(sendMu.Unlock)
	var workers sync.WaitGroup
	t.Cleanup(func() {
		releaseSendMu()
		workers.Wait()
	})

	observed := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		observed <- m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{
			Fetched: true, URL: "pr1", Number: 7, CI: domain.CIFailing,
			Checks: []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
		})
	}()
	select {
	case <-msgr.entered: // sendOnce holds react.mu and now waits for sendMu
	case <-time.After(5 * time.Second):
		t.Fatal("CI nudge never reached the messenger")
	}
	done := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		done <- m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityIdle})
		releaseSendMu()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("deadlock: turn-end ApplyActivitySignal blocks on react.mu while sendOnce holds it waiting for sendMu")
	}

	// Once the stand-in sendMu is free, the CI nudge still goes out.
	select {
	case err := <-observed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("CI nudge send never completed after sendMu was released")
	}
	m.deferredDeliveries.Wait()
	sendMu.Lock()
	msgs := append([]string(nil), msgr.msgs...)
	sendMu.Unlock()
	if len(msgs) != 1 || !strings.Contains(msgs[0], "boom") {
		t.Fatalf("msgs = %v, want the CI nudge delivered", msgs)
	}
}
