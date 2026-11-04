package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// B541: a kill right after a merge raced an automatic chat recovery holding
// the session and failed with a holder-less 409.
func TestKillQueuesBehindAutomaticOperations(t *testing.T) {
	for _, tc := range []struct {
		name string
		hold func(m *Manager, id domain.SessionID) func()
	}{
		{"reconcile", func(m *Manager, id domain.SessionID) func() {
			if err := m.beginAgentOperation(context.Background(), id, agentOperationReconcile); err != nil {
				t.Fatal(err)
			}
			return func() { m.endAgentOperation(id, agentOperationReconcile) }
		}},
		{"auto resume", func(m *Manager, id domain.SessionID) func() {
			markAutoResume(m, id)
			if err := m.beginAgentOperation(context.Background(), id, agentOperationResume); err != nil {
				t.Fatal(err)
			}
			return func() { m.endAgentOperation(id, agentOperationResume) }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newInputLeaseTestManager()
			id := domain.SessionID("worker-1")
			release := tc.hold(m, id)
			acquired := make(chan error, 1)
			go func() { acquired <- m.beginAgentOperation(context.Background(), id, agentOperationKill) }()
			select {
			case err := <-acquired:
				t.Fatalf("kill did not wait for %s: %v", tc.name, err)
			case <-time.After(50 * time.Millisecond):
			}
			release()
			if err := <-acquired; err != nil {
				t.Fatalf("kill after %s ended: %v", tc.name, err)
			}
			m.endAgentOperation(id, agentOperationKill)
		})
	}
}

func TestKillBusyErrorNamesHolderAfterWait(t *testing.T) {
	old := killYieldWait
	killYieldWait = 40 * time.Millisecond
	t.Cleanup(func() { killYieldWait = old })

	m := newInputLeaseTestManager()
	id := domain.SessionID("worker-1")
	if err := m.beginAgentOperation(context.Background(), id, agentOperationReconcile); err != nil {
		t.Fatal(err)
	}
	defer m.endAgentOperation(id, agentOperationReconcile)
	err := m.beginAgentOperation(context.Background(), id, agentOperationKill)
	if !errors.Is(err, errAgentOperationInProgress) {
		t.Fatalf("err = %v, want busy", err)
	}
	mapped := m.mapOperationInProgress(id, err)
	var busy *OperationInProgressError
	if !errors.As(mapped, &busy) || !errors.Is(mapped, ErrOperationInProgress) {
		t.Fatalf("mapped = %#v, want OperationInProgressError", mapped)
	}
	if busy.Holder != string(agentOperationReconcile) || busy.HeldFor < 40*time.Millisecond {
		t.Fatalf("holder = %q held %s", busy.Holder, busy.HeldFor)
	}
	if !strings.Contains(mapped.Error(), "reconcile") {
		t.Fatalf("message %q does not name the holder", mapped.Error())
	}
}

func TestKillFailsFastBehindExplicitOperation(t *testing.T) {
	m := newInputLeaseTestManager()
	id := domain.SessionID("worker-1")
	if err := m.beginAgentOperation(context.Background(), id, agentOperationResume); err != nil {
		t.Fatal(err)
	}
	defer m.endAgentOperation(id, agentOperationResume)
	start := time.Now()
	err := m.beginAgentOperation(context.Background(), id, agentOperationKill)
	if !errors.Is(err, errAgentOperationInProgress) || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %s, want immediate busy", err, time.Since(start))
	}
	var busy *OperationInProgressError
	if !errors.As(m.mapOperationInProgress(id, err), &busy) || busy.Holder != string(agentOperationResume) {
		t.Fatalf("holder = %+v, want user resume", busy)
	}
}

func markAutoResume(m *Manager, id domain.SessionID) {
	m.agentOpMu.Lock()
	defer m.agentOpMu.Unlock()
	if m.autoResumes == nil {
		m.autoResumes = make(map[domain.SessionID]int)
	}
	m.autoResumes[id]++
}

// Review on #967: a batch live pass reserves every candidate before its
// bounded workers dequeue them; a session still queued must give its slot to
// Kill at once instead of holding it for queue wait plus the check budget.
func TestKillTakesOverQueuedReconcileSlot(t *testing.T) {
	m, st, rt, _ := newManager()
	m.reconcileWorkers = 1
	for _, id := range []domain.SessionID{"s1", "s2"} {
		st.sessions[id] = domain.SessionRecord{ID: id, ProjectID: "mer", Harness: domain.HarnessClaudeCode,
			Activity: domain.Activity{State: domain.ActivityActive},
			Metadata: domain.SessionMetadata{Branch: "tp/" + string(id), WorkspacePath: "/wt/" + string(id), RuntimeHandleID: string(id)}}
	}
	probe := &queuedReconcileRuntime{fakeRuntime: rt, entered: make(chan string, 2), release: make(chan struct{})}
	m.runtime = probe
	recs := []domain.SessionRecord{st.sessions["s1"], st.sessions["s2"]}
	done := make(chan struct{})
	go func() { m.reconcileLivePass(context.Background(), recs); close(done) }()
	first := <-probe.entered
	queued := domain.SessionID("s2")
	if first == "s2" {
		queued = "s1"
	}
	start := time.Now()
	if err := m.beginAgentOperation(context.Background(), queued, agentOperationKill); err != nil {
		t.Fatalf("kill on queued session: %v", err)
	}
	if waited := time.Since(start); waited > time.Second {
		t.Fatalf("kill waited %s behind a queued reconcile", waited)
	}
	close(probe.release)
	<-done
	probe.mu.Lock()
	probed := append([]string(nil), probe.probed...)
	probe.mu.Unlock()
	for _, id := range probed {
		if id == string(queued) {
			t.Fatalf("pass probed %s after Kill took its slot", queued)
		}
	}
	m.agentOpMu.Lock()
	holder := m.agentOperations[queued]
	m.agentOpMu.Unlock()
	if holder != agentOperationKill {
		t.Fatalf("holder = %q, want kill (the pass must not release Kill's slot)", holder)
	}
	m.endAgentOperation(queued, agentOperationKill)
}

type queuedReconcileRuntime struct {
	*fakeRuntime
	mu      sync.Mutex
	probed  []string
	entered chan string
	release chan struct{}
}

func (r *queuedReconcileRuntime) IsAlive(_ context.Context, handle ports.RuntimeHandle) (bool, error) {
	r.mu.Lock()
	r.probed = append(r.probed, handle.ID)
	r.mu.Unlock()
	r.entered <- handle.ID
	<-r.release
	return true, nil
}
