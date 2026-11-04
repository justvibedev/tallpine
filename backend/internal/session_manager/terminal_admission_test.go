package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type admissionSessions map[domain.SessionID]domain.SessionRecord

func (s admissionSessions) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := s[id]
	return rec, ok, nil
}

type admissionRuntime struct {
	mu       sync.Mutex
	sent     []string
	screen   string
	inflight atomic.Int32
	maxSeen  atomic.Int32
	delay    time.Duration
}

func (r *admissionRuntime) SendMessage(_ context.Context, _ ports.RuntimeHandle, message string) error {
	n := r.inflight.Add(1)
	for {
		m := r.maxSeen.Load()
		if n <= m || r.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(r.delay)
	r.inflight.Add(-1)
	r.mu.Lock()
	r.sent = append(r.sent, message)
	r.mu.Unlock()
	return nil
}

func (r *admissionRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.screen, nil
}

func (r *admissionRuntime) sentCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sent)
}

// admissionAgent embeds the interface; only the optional policy methods are used.
type admissionAgent struct {
	ports.Agent
	refuse   error
	observed func(message, baseline, screen string) bool
}

func (a admissionAgent) ValidateTerminalMessage(string, string) error { return a.refuse }
func (a admissionAgent) TerminalMessageObserved(m, b, s string) bool {
	return a.observed != nil && a.observed(m, b, s)
}

type admissionResolver struct{ agent ports.Agent }

func (r admissionResolver) Agent(domain.AgentHarness) (ports.Agent, bool) { return r.agent, true }

func liveSession(id string) domain.SessionRecord {
	rec := domain.SessionRecord{ID: domain.SessionID(id), Harness: domain.HarnessClaudeCode}
	rec.Metadata.RuntimeHandleID = "h-" + id
	rec.Metadata.RuntimeLaunchID = "l-" + id
	return rec
}

func newAdmission(agent ports.Agent, rt *admissionRuntime, recs ...domain.SessionRecord) *TerminalAdmission {
	sessions := admissionSessions{}
	for _, r := range recs {
		sessions[r.ID] = r
	}
	a := NewTerminalAdmission(sessions, rt, admissionResolver{agent})
	a.witnessWait = 100 * time.Millisecond
	return a
}

func TestTerminalAdmissionConfirmedSendLeavesNoState(t *testing.T) {
	rt := &admissionRuntime{screen: "idle"}
	a := newAdmission(admissionAgent{observed: func(string, string, string) bool { return true }}, rt, liveSession("s1"))
	if err := a.Send(context.Background(), "s1", "hi"); err != nil {
		t.Fatal(err)
	}
	if rt.sentCount() != 1 || a.tracked() != 0 {
		t.Fatalf("sent=%d tracked=%d, want 1 and 0", rt.sentCount(), a.tracked())
	}
}

func TestTerminalAdmissionUnconfirmedSubmissionBlocksNextSend(t *testing.T) {
	rt := &admissionRuntime{screen: "idle"}
	a := newAdmission(admissionAgent{}, rt, liveSession("s1"))
	err := a.Send(context.Background(), "s1", "first")
	if err == nil || !strings.Contains(err.Error(), "unconfirmed") {
		t.Fatalf("first send err = %v, want unconfirmed", err)
	}
	err = a.Send(context.Background(), "s1", "second")
	if err == nil || !strings.Contains(err.Error(), "prior terminal submission remains unconfirmed") {
		t.Fatalf("second send err = %v, want prior-unconfirmed", err)
	}
	if rt.sentCount() != 1 || a.tracked() != 1 {
		t.Fatalf("sent=%d tracked=%d; the pending state must persist and block the write", rt.sentCount(), a.tracked())
	}
}

func TestTerminalAdmissionNewRuntimeLaunchClearsPending(t *testing.T) {
	rt := &admissionRuntime{screen: "idle"}
	rec := liveSession("s1")
	sessions := admissionSessions{"s1": rec}
	a := NewTerminalAdmission(sessions, rt, admissionResolver{admissionAgent{}})
	a.witnessWait = 50 * time.Millisecond
	_ = a.Send(context.Background(), "s1", "first")
	rec.Metadata.RuntimeLaunchID = "l-new"
	sessions["s1"] = rec
	err := a.Send(context.Background(), "s1", "second")
	if err == nil || strings.Contains(err.Error(), "prior terminal submission") {
		t.Fatalf("a new launch must reset pending, got %v", err)
	}
	if rt.sentCount() != 2 {
		t.Fatalf("sent=%d, want 2", rt.sentCount())
	}
}

func TestTerminalAdmissionScreenValidationRefusesBeforeWriting(t *testing.T) {
	rt := &admissionRuntime{screen: "dialog open"}
	refusal := errors.New("a dialog is open")
	a := newAdmission(admissionAgent{refuse: refusal}, rt, liveSession("s1"))
	if err := a.Send(context.Background(), "s1", "hi"); !errors.Is(err, refusal) {
		t.Fatalf("err = %v, want the policy refusal", err)
	}
	if rt.sentCount() != 0 || a.tracked() != 0 {
		t.Fatalf("sent=%d tracked=%d; a refused message must write nothing and leave no state", rt.sentCount(), a.tracked())
	}
}

func TestTerminalAdmissionSerializesSendsPerSession(t *testing.T) {
	rt := &admissionRuntime{screen: "idle", delay: 20 * time.Millisecond}
	a := newAdmission(admissionAgent{observed: func(string, string, string) bool { return true }}, rt, liveSession("s1"))
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = a.Send(context.Background(), "s1", "m")
		}()
	}
	wg.Wait()
	if rt.maxSeen.Load() != 1 || rt.sentCount() != 6 {
		t.Fatalf("max concurrent writes=%d sent=%d, want 1 and 6", rt.maxSeen.Load(), rt.sentCount())
	}
	if a.tracked() != 0 {
		t.Fatalf("tracked=%d after all sends confirmed", a.tracked())
	}
}

func TestTerminalAdmissionStateStaysBoundedAcrossSessions(t *testing.T) {
	rt := &admissionRuntime{screen: "idle"}
	sessions := admissionSessions{}
	for i := 0; i < 50; i++ {
		sessions[domain.SessionID(fmt.Sprintf("s%d", i))] = liveSession(fmt.Sprintf("s%d", i))
	}
	a := NewTerminalAdmission(sessions, rt, admissionResolver{admissionAgent{observed: func(string, string, string) bool { return true }}})
	for id := range sessions {
		if err := a.Send(context.Background(), id, "hi"); err != nil {
			t.Fatal(err)
		}
		rec := sessions[id]
		rec.IsTerminated = true
		sessions[id] = rec
		if err := a.Send(context.Background(), id, "again"); !errors.Is(err, ErrTerminated) {
			t.Fatalf("terminated send err = %v", err)
		}
	}
	if a.tracked() != 0 {
		t.Fatalf("tracked=%d after 50 terminated sessions, want 0", a.tracked())
	}
}

func TestTerminalAdmissionMissingSessionAndHandle(t *testing.T) {
	rt := &admissionRuntime{}
	a := NewTerminalAdmission(admissionSessions{"s1": {ID: "s1"}}, rt, nil)
	if err := a.Send(context.Background(), "nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if err := a.Send(context.Background(), "s1", "x"); !errors.Is(err, ErrIncompleteHandle) {
		t.Fatalf("err = %v", err)
	}
}

// A sender that sets pending and returns while another sender is between
// unlocking and dropping its reference must not have its pending state deleted.
func TestTerminalAdmissionReleaseKeepsPendingSetByConcurrentSender(t *testing.T) {
	rt := &admissionRuntime{screen: "idle"}
	a := newAdmission(admissionAgent{}, rt, liveSession("s1")) // never witnessed -> leaves pending
	first := a.acquire("s1")
	a.releaseHook = func() {
		a.releaseHook = nil
		// While first sits between unlock and dropping its ref, a second send
		// completes unconfirmed (sets pending, releases).
		_ = a.Send(context.Background(), "s1", "second")
	}
	a.release("s1", first)
	if a.tracked() != 1 {
		t.Fatalf("tracked=%d; an entry with a pending submission must survive release", a.tracked())
	}
	if err := a.Send(context.Background(), "s1", "third"); err == nil || !strings.Contains(err.Error(), "prior terminal submission remains unconfirmed") {
		t.Fatalf("third send err = %v, want prior-unconfirmed", err)
	}
}
