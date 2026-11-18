package sessionmanager

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// B810: the delays after the schedule back off to the cap and never end.
func TestStartupChatRetryDelayBacksOffToCap(t *testing.T) {
	t.Parallel()

	m := &Manager{
		startupChatRetryDelays:   []time.Duration{time.Second, 2 * time.Second},
		startupChatRetryMaxDelay: 10 * time.Second,
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}
	var delay time.Duration
	for attempt, expected := range want {
		delay = m.startupChatRetryDelay(attempt, delay)
		if delay != expected {
			t.Fatalf("attempt %d delay = %s, want %s", attempt, delay, expected)
		}
	}
	if got := (&Manager{}).startupChatRetryDelay(0, 0); got != defaultStartupChatRetryMaxDelay {
		t.Fatalf("delay without a schedule or cap = %s, want the default cap", got)
	}
}

// B810: when the schedule is spent the session reads unavailable so Resume is
// offered, but the reattach keeps trying instead of leaving it dead until the
// next daemon restart.
func TestStartupChatCheckRetryContinuesAfterSchedule(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, st, _, _ := newLifecycleManager()
	launcher := &flakyStartupLauncher{failures: 4, err: ports.ErrChatRecoveryInconclusive}
	m.chat = launcher
	m.startupChatRetryDelays = []time.Duration{time.Millisecond, 100 * time.Millisecond}
	m.startupChatRetryMaxDelay = 200 * time.Millisecond
	rec := startupChatRecord()
	st.sessions[rec.ID] = rec
	if err := m.ReconcileBackground(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "every scheduled retry", func() bool { return launcher.attemptCount() >= 3 })
	if got := m.SessionStatusReadiness(st.sessions[rec.ID]); got != "unavailable" {
		t.Fatalf("readiness = %s, want unavailable so Resume is offered", got)
	}
	waitFor(t, "a retry past the schedule to reattach", func() bool { return launcher.HasLiveChatController(rec.ID) })
	waitFor(t, "ready readiness", func() bool { return m.SessionStatusReadiness(st.sessions[rec.ID]) == "ready" })
	if got := launcher.attemptCount(); got != 5 {
		t.Fatalf("attempts = %d, want the startup check, two scheduled retries and two backoff retries", got)
	}
}

// B810: once the schedule is spent the owner's Resume is the way back, and it
// must reattach rather than refuse while the background retry waits out its
// backoff.
func TestResumeReattachesAChatWhoseStartupReattachFailed(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, st, _, _ := newLifecycleManager()
	launcher := &flakyStartupLauncher{failures: 3, err: ports.ErrChatRecoveryInconclusive}
	m.chat = launcher
	m.startupChatRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	m.startupChatRetryMaxDelay = time.Hour
	rec := startupChatRecord()
	st.sessions[rec.ID] = rec
	if err := m.ReconcileBackground(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the schedule to be spent", func() bool {
		return m.SessionStatusReadiness(st.sessions[rec.ID]) == "unavailable"
	})
	if _, err := m.ResumeAgentWithMode(ctx, rec.ID); err != nil {
		t.Fatalf("Resume after a failed reattach: %v", err)
	}
	if !launcher.HasLiveChatController(rec.ID) {
		t.Fatal("Resume did not reattach the chat")
	}
	if got := m.SessionStatusReadiness(st.sessions[rec.ID]); got != "ready" {
		t.Fatalf("readiness after Resume = %s, want ready", got)
	}
}
