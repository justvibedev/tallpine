package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// B627: only workers cut off mid-turn are in the restart tracker, so an idle
// worker whose resume gives up must be reported from here or nobody hears.
func TestStartupChatResumeGiveUpTellsOrchestrator(t *testing.T) {
	t.Parallel()

	permanent := fmt.Errorf("model gone: %w", ErrResumeModelDenied)
	for name, tc := range map[string]struct {
		attempt  func(context.Context, domain.SessionID) (bool, error)
		cancel   bool
		wantSent bool
	}{
		"retries exhausted": {attempt: func(context.Context, domain.SessionID) (bool, error) {
			return false, errors.New("provider refused")
		}, wantSent: true},
		"permanent failure": {attempt: func(context.Context, domain.SessionID) (bool, error) {
			return true, permanent
		}, wantSent: true},
		"session no longer wants it": {attempt: func(context.Context, domain.SessionID) (bool, error) {
			return true, ErrTerminated
		}},
		"resumed": {attempt: func(context.Context, domain.SessionID) (bool, error) {
			return true, nil
		}},
		// Canceled inside the attempt: a context already done before it would let
		// the semaphore select return early and never reach the report.
		"daemon shutting down": {attempt: nil, cancel: true},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m, _, _, _ := newLifecycleManager()
			m.startupChatResumeDelays = []time.Duration{time.Millisecond}
			m.startupChatResumeSem = make(chan struct{}, 1)
			m.startupChatResume = tc.attempt
			if tc.cancel {
				m.startupChatResume = func(context.Context, domain.SessionID) (bool, error) {
					cancel()
					return false, errors.New("context canceled")
				}
			}
			var sent []string
			m.startFailed = func(id domain.SessionID, reason string) { sent = append(sent, string(id)+": "+reason) }

			m.resumeStartupChat(ctx, "mer-chat")

			if got := len(sent) == 1; got != tc.wantSent {
				t.Fatalf("reports = %q, want sent=%v", sent, tc.wantSent)
			}
			if tc.wantSent && !strings.Contains(sent[0], "mer-chat: could not resume") {
				t.Fatalf("report = %q", sent[0])
			}
		})
	}
}

type startFailedLog struct {
	mu   sync.Mutex
	sent []domain.SessionID
}

func (l *startFailedLog) observe(id domain.SessionID, _ string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sent = append(l.sent, id)
}

func (l *startFailedLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.sent)
}

// B627: the plain Force Quit case, where the host survived but the reattach
// never succeeds. The tracker does not know an idle worker, so this is the only
// way its orchestrator hears that it is not running.
func TestStartupReattachRetriesExhaustedTellsOrchestrator(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, st, _, _ := newLifecycleManager()
	launcher := &flakyStartupLauncher{failures: -1, err: ports.ErrChatRecoveryInconclusive}
	m.chat = launcher
	m.startupChatRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	log := &startFailedLog{}
	m.startFailed = log.observe
	rec := startupChatRecord()
	st.sessions[rec.ID] = rec
	if err := m.ReconcileBackground(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the give-up report", func() bool { return log.count() > 0 })
	if got := launcher.attemptCount(); got != 3 {
		t.Fatalf("reported after %d attempts, want only after the last of 3", got)
	}
	time.Sleep(20 * time.Millisecond)
	if got := log.count(); got != 1 {
		t.Fatalf("reports = %d, want 1", got)
	}
}

func TestStartupLivePassNonRetryableFailureTellsOrchestrator(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newLifecycleManager()
	launcher := &flakyStartupLauncher{failures: -1, err: errors.New("provider config broken")}
	m.chat = launcher
	m.startupChatRetryDelays = []time.Duration{time.Millisecond}
	log := &startFailedLog{}
	m.startFailed = log.observe
	rec := startupChatRecord()
	st.sessions[rec.ID] = rec
	if err := m.ReconcileBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the report", func() bool { return log.count() > 0 })
	time.Sleep(20 * time.Millisecond)
	if attempts, reports := launcher.attemptCount(), log.count(); attempts != 1 || reports != 1 {
		t.Fatalf("attempts = %d, reports = %d, want one of each", attempts, reports)
	}
}

func TestStartupReattachReportDecisions(t *testing.T) {
	t.Parallel()

	inconclusive := ports.ErrChatRecoveryInconclusive
	permanent := errors.New("provider config broken")
	for name, tc := range map[string]struct {
		err        error
		final      bool
		mutate     func(*domain.SessionRecord)
		userOwned  bool
		wantRetry  bool
		wantReport bool
	}{
		"retryable with attempts left stays quiet": {err: inconclusive, wantRetry: true},
		"retryable on the last attempt reports":    {err: inconclusive, final: true, wantReport: true},
		"non-retryable reports at once":            {err: permanent, wantReport: true},
		"user-stopped agent stays quiet": {err: permanent, mutate: func(r *domain.SessionRecord) {
			r.Activity.State = domain.ActivityExited
		}},
		"user operation owns it stays quiet": {err: permanent, final: true, userOwned: true},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			m, st, _, _ := newLifecycleManager()
			m.chat = &flakyStartupLauncher{failures: -1, err: tc.err}
			log := &startFailedLog{}
			m.startFailed = log.observe
			rec := startupChatRecord()
			if tc.mutate != nil {
				tc.mutate(&rec)
			}
			st.sessions[rec.ID] = rec
			if tc.userOwned {
				if err := m.beginAgentOperation(ctx, rec.ID, agentOperationReconcile); err != nil {
					t.Fatal(err)
				}
				defer m.endAgentOperation(rec.ID, agentOperationReconcile)
			}
			retry := m.retryStartupChatCheck(ctx, rec.ID, tc.final)
			if retry != tc.wantRetry {
				t.Fatalf("retry = %v, want %v", retry, tc.wantRetry)
			}
			if got := log.count() == 1; got != tc.wantReport {
				t.Fatalf("reported = %v, want %v", got, tc.wantReport)
			}
		})
	}
}

func TestReportStartupReattachFailedGuards(t *testing.T) {
	t.Parallel()

	asleep := time.Now()
	for name, tc := range map[string]struct {
		mutate     func(*domain.SessionRecord)
		reason     domain.AgentExitReason
		wantReport bool
	}{
		"live chat reports": {wantReport: true},
		"lost host reports": {reason: domain.AgentExitHostLost, wantReport: true, mutate: func(r *domain.SessionRecord) {
			r.Activity.State = domain.ActivityExited
		}},
		"stopped by choice is quiet": {mutate: func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityExited }},
		"asleep is quiet":            {mutate: func(r *domain.SessionRecord) { r.HibernatedAt = &asleep }},
		"terminal session is quiet":  {mutate: func(r *domain.SessionRecord) { r.Mode = domain.SessionModeTUI }},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			m, st, _, _ := newLifecycleManager()
			log := &startFailedLog{}
			m.startFailed = log.observe
			rec := startupChatRecord()
			if tc.mutate != nil {
				tc.mutate(&rec)
			}
			st.sessions[rec.ID] = rec
			if tc.reason != "" {
				m.setAgentExitReason(ctx, rec.ID, tc.reason)
			}
			m.reportStartupReattachFailed(ctx, rec, errors.New("boom"))
			if got := log.count() == 1; got != tc.wantReport {
				t.Fatalf("reported = %v, want %v", got, tc.wantReport)
			}
		})
	}
}
