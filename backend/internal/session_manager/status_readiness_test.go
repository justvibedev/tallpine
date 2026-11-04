package sessionmanager

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestStatusReadinessWaitsForRecoveryAndAllowsRetry(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newManager()
	rec := domain.SessionRecord{ID: "s1", ProjectID: "mer", Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: time.Unix(100, 0)},
		Metadata: domain.SessionMetadata{Branch: "tp/s1", WorkspacePath: "/wt/s1", RuntimeHandleID: "s1"}}
	st.sessions[rec.ID] = rec
	if got := m.SessionStatusReadiness(rec); got != "checking" {
		t.Fatalf("before recovery = %s", got)
	}
	rt.aliveErr = errors.New("runtime probe unavailable")
	if err := m.ReconcileBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := m.SessionStatusReadiness(rec); got != "unavailable" {
		t.Fatalf("failed probe = %s", got)
	}
	if st.sessions[rec.ID].Activity != rec.Activity {
		t.Fatal("failed probe changed activity")
	}
	rt.aliveErr = nil
	rt.aliveByHandle = map[string]bool{"s1": true}
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatal(err)
	}
	if got := m.SessionStatusReadiness(st.sessions[rec.ID]); got != "ready" {
		t.Fatalf("retry = %s", got)
	}
	if rt.created != 0 {
		t.Fatal("retry spawned a duplicate of a surviving runtime")
	}
}

type stubbornAliveRuntime struct {
	*fakeRuntime
	entered chan domain.SessionID
	release chan struct{}
}

func (r *stubbornAliveRuntime) IsAlive(_ context.Context, handle ports.RuntimeHandle) (bool, error) {
	r.entered <- domain.SessionID(handle.ID)
	<-r.release
	return true, nil
}

func TestStatusReadinessDoesNotOfferRetryWhileRecoveryOwnsSession(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newManager()
	rec := domain.SessionRecord{ID: "s1", ProjectID: "mer", Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{Branch: "tp/s1", WorkspacePath: "/wt/s1", RuntimeHandleID: "s1"}}
	st.sessions[rec.ID] = rec
	blocked := &stubbornAliveRuntime{fakeRuntime: rt, entered: make(chan domain.SessionID, 1), release: make(chan struct{})}
	m.runtime = blocked
	m.statusVerificationLimit = time.Nanosecond
	finished := make(chan error, 1)
	go func() { finished <- m.ReconcileBackground(context.Background()) }()
	<-blocked.entered
	time.Sleep(time.Millisecond)
	if got := m.SessionStatusReadiness(rec); got != "checking" {
		t.Fatalf("owned recovery = %s, want checking until retry can acquire the session", got)
	}
	close(blocked.release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if got := m.SessionStatusReadiness(rec); got != "ready" {
		t.Fatalf("late recovery = %s", got)
	}
	if st.sessions[rec.ID].Activity != rec.Activity {
		t.Fatal("recovery invented activity")
	}
}

type deadlineAwareRuntime struct {
	*fakeRuntime
	entered chan struct{}
}

func (r *deadlineAwareRuntime) IsAlive(ctx context.Context, _ ports.RuntimeHandle) (bool, error) {
	close(r.entered)
	<-ctx.Done()
	return false, ctx.Err()
}

func TestStatusReadinessDeadlineReleasesSessionForRetry(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newManager()
	rec := domain.SessionRecord{ID: "s1", ProjectID: "mer", Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{Branch: "tp/s1", WorkspacePath: "/wt/s1", RuntimeHandleID: "s1"}}
	st.sessions[rec.ID] = rec
	deadlineRuntime := &deadlineAwareRuntime{fakeRuntime: rt, entered: make(chan struct{})}
	m.runtime = deadlineRuntime
	m.statusVerificationLimit = 10 * time.Millisecond
	finished := make(chan error, 1)
	go func() { finished <- m.ReconcileBackground(context.Background()) }()
	<-deadlineRuntime.entered
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if got := m.SessionStatusReadiness(rec); got != "unavailable" {
		t.Fatalf("deadline = %s, want unavailable", got)
	}
	rt.aliveByHandle = map[string]bool{"s1": true}
	m.runtime = rt
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatalf("retry after deadline: %v", err)
	}
	if got := m.SessionStatusReadiness(st.sessions[rec.ID]); got != "ready" {
		t.Fatalf("retry = %s, want ready", got)
	}
}

func TestStatusReadinessDiscoveryFailureIsUnavailable(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	st.listAllErr = errors.New("storage unavailable")
	if err := m.ReconcileBackground(context.Background()); err == nil {
		t.Fatal("expected discovery failure")
	}
	if got := m.SessionStatusReadiness(domain.SessionRecord{ID: "s1"}); got != "unavailable" {
		t.Fatalf("readiness = %s", got)
	}
}

func TestStatusReadinessFreshSpawnAfterDiscoveryFailureIsReady(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	st.listAllErr = errors.New("storage unavailable")
	if err := m.ReconcileBackground(context.Background()); err == nil {
		t.Fatal("expected discovery failure")
	}
	st.listAllErr = nil
	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer",
		Kind:      domain.KindWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.SessionStatusReadiness(rec); got != "ready" {
		t.Fatalf("fresh spawn readiness = %s, want ready", got)
	}
}

func TestStatusReadinessFreshSpawnAfterSuccessfulRecoveryDoesNotChangeRevision(t *testing.T) {
	t.Parallel()

	m, _, _, _ := newManager()
	if err := m.ReconcileBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	revision := m.StatusRecoveryRevision()
	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer",
		Kind:      domain.KindWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.StatusRecoveryRevision(); got != revision {
		t.Fatalf("fresh spawn recovery revision = %d, want %d", got, revision)
	}
	if got := m.SessionStatusReadiness(rec); got != "ready" {
		t.Fatalf("fresh spawn readiness = %s, want ready", got)
	}
}

func TestStatusReadinessTerminatedSessionsAreReadyImmediately(t *testing.T) {
	t.Parallel()

	m, _, _, _ := newManager()
	rec := domain.SessionRecord{ID: "done", ProjectID: "mer", Harness: domain.HarnessClaudeCode, IsTerminated: true,
		Metadata: domain.SessionMetadata{RuntimeHandleID: "done"}}
	if got := m.SessionStatusReadiness(rec); got != "ready" {
		t.Fatalf("terminated before startup reconcile = %s, want ready", got)
	}
	live := rec
	live.IsTerminated = false
	if got := m.SessionStatusReadiness(live); got != "checking" {
		t.Fatalf("live before startup reconcile = %s, want checking", got)
	}
}

// reapProbeRuntime blocks IsAlive for the named handles so a test can observe
// state while the terminated-session reap is still in flight.
type reapProbeRuntime struct {
	*fakeRuntime
	mu      sync.Mutex
	probed  []string
	entered chan string
	release chan struct{}
}

func (r *reapProbeRuntime) IsAlive(_ context.Context, handle ports.RuntimeHandle) (bool, error) {
	r.mu.Lock()
	r.probed = append(r.probed, handle.ID)
	r.mu.Unlock()
	if handle.ID == "dead-1" || handle.ID == "dead-2" {
		r.entered <- handle.ID
		<-r.release
	}
	return false, nil
}

func TestReconcileBackgroundFlipsReadinessBeforeReapingTerminatedInParallel(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newManager()
	m.reconcileWorkers = 2
	live := domain.SessionRecord{ID: "live", ProjectID: "mer", Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{Branch: "tp/live", WorkspacePath: "/wt/live", RuntimeHandleID: "live"}}
	st.sessions[live.ID] = live
	for _, id := range []domain.SessionID{"dead-1", "dead-2"} {
		st.sessions[id] = domain.SessionRecord{ID: id, ProjectID: "mer", Harness: domain.HarnessClaudeCode, IsTerminated: true,
			Metadata: domain.SessionMetadata{RuntimeHandleID: string(id)}}
	}
	// No runtime handle: nothing can have leaked, so the reap must not probe it.
	st.sessions["dead-nohandle"] = domain.SessionRecord{ID: "dead-nohandle", ProjectID: "mer", Harness: domain.HarnessClaudeCode, IsTerminated: true}

	probe := &reapProbeRuntime{fakeRuntime: rt, entered: make(chan string, 2), release: make(chan struct{})}
	m.runtime = probe
	notified := make(chan struct{}, 8)
	unsubscribe := m.SubscribeStatusReadiness(func() { notified <- struct{}{} })
	defer unsubscribe()

	finished := make(chan error, 1)
	go func() { finished <- m.ReconcileBackground(context.Background()) }()

	// Both terminated probes are in flight at once (parallel) ...
	for range 2 {
		select {
		case <-probe.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("terminated sessions were not reaped in parallel")
		}
	}
	// ... and readiness has already flipped while they are still blocked.
	if got := m.SessionStatusReadiness(live); got != "ready" {
		t.Fatalf("live readiness during reap = %s, want ready", got)
	}
	select {
	case <-notified:
	default:
		t.Fatal("subscribers were not notified when readiness settled")
	}
	close(probe.release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	probe.mu.Lock()
	defer probe.mu.Unlock()
	for _, id := range probe.probed {
		if id == "" {
			t.Fatal("probed a session with an empty runtime handle")
		}
	}
}

func TestStatusRecoveriesForgetSettledSuccesses(t *testing.T) {
	t.Parallel()

	m, st, rt, _ := newManager()
	recs := []domain.SessionRecord{}
	for _, id := range []domain.SessionID{"s1", "s2"} {
		rec := domain.SessionRecord{ID: id, ProjectID: "mer", Harness: domain.HarnessClaudeCode,
			Activity: domain.Activity{State: domain.ActivityActive},
			Metadata: domain.SessionMetadata{Branch: "tp/" + string(id), WorkspacePath: "/wt/" + string(id), RuntimeHandleID: string(id)}}
		st.sessions[id] = rec
		recs = append(recs, rec)
	}
	rt.aliveByHandle = map[string]bool{"s1": true, "s2": true}
	// A session spawned before startup settles needs its entry until then.
	m.markFreshSessionStatusReady("fresh")
	if err := m.ReconcileBackground(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, rec := range recs {
		if got := m.SessionStatusReadiness(st.sessions[rec.ID]); got != "ready" {
			t.Fatalf("%s readiness = %s, want ready", rec.ID, got)
		}
	}
	if got := m.SessionStatusReadiness(domain.SessionRecord{ID: "fresh"}); got != "ready" {
		t.Fatalf("fresh readiness = %s, want ready", got)
	}
	m.statusRecoveryMu.RLock()
	left := len(m.statusRecoveries)
	m.statusRecoveryMu.RUnlock()
	if left != 0 {
		t.Fatalf("settled successful recoveries kept %d entries, want 0", left)
	}

	// A later successful recovery does not leave an entry behind either.
	m.beginStatusRecovery("s1")
	if got := m.SessionStatusReadiness(st.sessions["s1"]); got != "checking" {
		t.Fatalf("in-flight recovery = %s, want checking", got)
	}
	m.finishStatusRecovery(context.Background(), st.sessions["s1"], nil)
	if got := m.SessionStatusReadiness(st.sessions["s1"]); got != "ready" {
		t.Fatalf("recovered = %s, want ready", got)
	}
	m.statusRecoveryMu.RLock()
	left = len(m.statusRecoveries)
	m.statusRecoveryMu.RUnlock()
	if left != 0 {
		t.Fatalf("post-startup recovery kept %d entries, want 0", left)
	}
}
