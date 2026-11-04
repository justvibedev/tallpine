package sessionmanager

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionguard"
)

// coldWakingLauncher behaves like the Chat service for a hibernated session: a
// relay to a cold controller wakes the session through the manager first.
type coldWakingLauncher struct {
	*recordingLauncher
	mgr  *Manager
	warm atomic.Bool
}

func (l *coldWakingLauncher) StartChat(ctx context.Context, cfg ChatStart) (ChatStarted, error) {
	started, err := l.recordingLauncher.StartChat(ctx, cfg)
	if err == nil {
		l.warm.Store(true)
	}
	return started, err
}

func (l *coldWakingLauncher) HasLiveChatController(domain.SessionID) bool { return l.warm.Load() }

func (l *coldWakingLauncher) RelaySessionChatTurn(ctx context.Context, id domain.SessionID, text, clientMessageID string, options ports.MessageDeliveryOptions) (string, error) {
	if !l.warm.Load() {
		if err := l.mgr.WakeHibernatedChat(ctx, id); err != nil {
			return "", err
		}
		l.warm.Store(true)
	}
	return l.recordingLauncher.RelaySessionChatTurn(ctx, id, text, clientMessageID, options)
}

type managerSender struct{ mgr *Manager }

func (s managerSender) Send(ctx context.Context, id domain.SessionID, msg string) error {
	return s.mgr.Send(ctx, id, msg, nil)
}

// A lifecycle nudge (observer reaction, review notice) to a chat parked for quit
// goes through the guard, which holds an input lease across the write. The wake
// the write triggers is an exclusive operation waiting for leases to drain, so
// under that lease it waited for itself until its deadline. The guard must wake
// the chat before taking the lease.
func TestGuardedNudgeToParkedChatWakesItWithinSeconds(t *testing.T) {
	t.Parallel()
	base := &recordingLauncher{}
	launcher := &coldWakingLauncher{recordingLauncher: base}
	mgr, store, _ := newChatManager(launcher)
	launcher.mgr = mgr
	seedChatResumeSession(store, domain.ActivityIdle)
	rec := store.sessions["mer-1"]
	at := time.Now().UTC()
	rec.HibernatedAt = &at
	store.sessions[rec.ID] = rec

	guard := sessionguard.New(store, managerSender{mgr}, nil)
	guard.SetInputLease(mgr)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	outcome, err := guard.Nudge(ctx, rec.ID, "address the review comments")
	if err != nil || outcome != sessionguard.Sent {
		t.Fatalf("nudge outcome=%v err=%v after %v, want Sent", outcome, err, time.Since(start))
	}
	if len(base.relayed) != 1 || !strings.Contains(base.relayed[0], "review comments") {
		t.Fatalf("relayed = %q, want the nudge delivered after the wake", base.relayed)
	}
	if len(base.started) == 0 {
		t.Fatal("the parked chat was never restarted")
	}
}

func TestPrepareSessionInputLeavesStoppedChatStopped(t *testing.T) {
	t.Parallel()
	base := &recordingLauncher{}
	launcher := &coldWakingLauncher{recordingLauncher: base}
	mgr, store, _ := newChatManager(launcher)
	launcher.mgr = mgr
	seedChatResumeSession(store, domain.ActivityExited) // stopped on purpose: not hibernated
	mgr.PrepareSessionInput(context.Background(), "mer-1")
	if len(base.started) != 0 {
		t.Fatalf("started = %v, want a stopped chat left alone", base.started)
	}
}

func TestAgentOperationTimeoutNamesTheHolder(t *testing.T) {
	t.Parallel()
	mgr, _, _ := newChatManager(&recordingLauncher{})
	release, ok := mgr.AcquireSessionInput("mer-1")
	if !ok {
		t.Fatal("lease refused")
	}
	defer release()
	var logged strings.Builder
	mgr.logger = slog.New(slog.NewTextHandler(&logged, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := mgr.beginAgentOperation(ctx, "mer-1", agentOperationResume); err == nil {
		t.Fatal("want a deadline error while a lease is outstanding")
	}
	for _, want := range []string{"agent operation wait timed out", "inputLeases=1", "oldestLeaseAge", "wanted=resume"} {
		if !strings.Contains(logged.String(), want) {
			t.Fatalf("log %q lacks %q", logged.String(), want)
		}
	}
}
