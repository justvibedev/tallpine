package accountadopt

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

type memAdoptions struct {
	createErr error
	mu        sync.Mutex
	batches   map[string]domain.AccountAdoption
	order     []string
}

func newMemAdoptions() *memAdoptions {
	return &memAdoptions{batches: map[string]domain.AccountAdoption{}}
}

func (m *memAdoptions) clone(b domain.AccountAdoption) domain.AccountAdoption {
	b.Items = append([]domain.AccountAdoptionItem(nil), b.Items...)
	return b
}
func (m *memAdoptions) CreateAccountAdoption(_ context.Context, b domain.AccountAdoption) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.batches {
		if o.AccountID == b.AccountID && o.RequestKey == b.RequestKey {
			return ports.ErrAccountAdoptionExists
		}
	}
	b = m.clone(b)
	b.State = domain.AccountAdoptionRunning
	for i := range b.Items {
		b.Items[i].State = domain.AccountItemQueued // as the real store inserts them
	}
	m.batches[b.ID] = m.clone(b)
	m.order = append(m.order, b.ID)
	return nil
}
func (m *memAdoptions) find(f func(domain.AccountAdoption) bool) (domain.AccountAdoption, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.order) - 1; i >= 0; i-- {
		if b := m.batches[m.order[i]]; f(b) {
			return m.clone(b), true
		}
	}
	return domain.AccountAdoption{}, false
}
func (m *memAdoptions) GetAccountAdoption(_ context.Context, id string) (domain.AccountAdoption, bool, error) {
	b, ok := m.find(func(b domain.AccountAdoption) bool { return b.ID == id })
	return b, ok, nil
}
func (m *memAdoptions) GetAccountAdoptionByKey(_ context.Context, a, k string) (domain.AccountAdoption, bool, error) {
	b, ok := m.find(func(b domain.AccountAdoption) bool { return b.AccountID == a && b.RequestKey == k })
	return b, ok, nil
}
func (m *memAdoptions) GetLatestAccountAdoption(_ context.Context, a string) (domain.AccountAdoption, bool, error) {
	b, ok := m.find(func(b domain.AccountAdoption) bool { return b.AccountID == a })
	return b, ok, nil
}
func (m *memAdoptions) GetRunningAccountAdoption(_ context.Context, p domain.AgentAccountProvider) (domain.AccountAdoption, bool, error) {
	b, ok := m.find(func(b domain.AccountAdoption) bool {
		return b.Provider == p && b.State == domain.AccountAdoptionRunning
	})
	return b, ok, nil
}
func (m *memAdoptions) ListRunningAccountAdoptions(context.Context) ([]domain.AccountAdoption, error) {
	var out []domain.AccountAdoption
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range m.order {
		if b := m.batches[id]; b.State == domain.AccountAdoptionRunning {
			out = append(out, m.clone(b))
		}
	}
	return out, nil
}
func (m *memAdoptions) UpdateAccountAdoptionItem(_ context.Context, id string, it domain.AccountAdoptionItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.batches[id]
	for i := range b.Items {
		if b.Items[i].SessionID == it.SessionID {
			b.Items[i].State, b.Items[i].TransitionID, b.Items[i].ErrorCode, b.Items[i].ErrorMessage = it.State, it.TransitionID, it.ErrorCode, it.ErrorMessage
		}
	}
	m.batches[id] = b
	return nil
}
func (m *memAdoptions) CompleteAccountAdoption(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.batches[id]
	b.State = domain.AccountAdoptionCompleted
	m.batches[id] = b
	return nil
}

// world fakes accounts, sessions and the per-session move.
type world struct {
	mu            sync.Mutex
	recs          []domain.SessionRecord
	bound         map[domain.SessionID]string
	defaultID     string
	calls         []string                  // ordered log: "default", "start:<id>", "commit:<id>"
	commitOnStart map[domain.SessionID]bool // the move commits its binding as it starts
	onStart       func()                    // runs as a move is asked for, before it is recorded
	policies      map[domain.SessionID]domain.SessionInterfaceTransitionPolicy
	active        map[domain.SessionID]domain.SessionInterfaceTransition // moves not started by the batch
	validate      error
	startErr      map[domain.SessionID]error
	phase         map[string]domain.SessionInterfaceTransitionPhase // by transition id
	initial       map[domain.SessionID]domain.SessionInterfaceTransitionPhase
	maxFlight     int
}

func newWorld() *world {
	return &world{commitOnStart: map[domain.SessionID]bool{}, policies: map[domain.SessionID]domain.SessionInterfaceTransitionPolicy{}, active: map[domain.SessionID]domain.SessionInterfaceTransition{}, bound: map[domain.SessionID]string{}, startErr: map[domain.SessionID]error{},
		phase: map[string]domain.SessionInterfaceTransitionPhase{}, initial: map[domain.SessionID]domain.SessionInterfaceTransitionPhase{}}
}

func (w *world) session(id string, kind domain.SessionKind, age int, activity domain.ActivityState) {
	w.recs = append(w.recs, domain.SessionRecord{
		ID: domain.SessionID(id), Kind: kind, Harness: domain.HarnessClaudeCode,
		CreatedAt: time.Unix(int64(1000+age), 0), Activity: domain.Activity{State: activity},
	})
	w.bound[domain.SessionID(id)] = "device-claude"
}

func (w *world) Account(_ context.Context, id string) (domain.AgentAccount, error) {
	if id == "acct-b" || id == "device-claude" {
		return domain.AgentAccount{ID: id, Provider: domain.AgentAccountProviderClaude, Home: "/tmp/" + id}, nil
	}
	if id == "codex-1" {
		return domain.AgentAccount{ID: id, Provider: domain.AgentAccountProviderCodex}, nil
	}
	return domain.AgentAccount{}, ports.ErrAgentAccountUnknown
}
func (w *world) ValidateAccount(context.Context, domain.AgentHarness, string) error {
	return w.validate
}
func (w *world) SetDefault(_ context.Context, id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.defaultID = id
	w.calls = append(w.calls, "default")
	return nil
}
func (w *world) SessionAccount(_ context.Context, id domain.SessionID, _ domain.AgentAccountProvider) (domain.AgentAccount, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return domain.AgentAccount{ID: w.bound[id]}, true, nil
}
func (w *world) CommitSession(_ context.Context, id domain.SessionID, _ domain.AgentHarness, account string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, "commit:"+string(id))
	w.bound[id] = account
	return nil
}
func (w *world) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]domain.SessionRecord(nil), w.recs...), nil
}
func (w *world) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, r := range w.recs {
		if r.ID == id {
			return r, true, nil
		}
	}
	return domain.SessionRecord{}, false, nil
}
func (w *world) StartAccountTransition(_ context.Context, id domain.SessionID, account string, policy domain.SessionInterfaceTransitionPolicy) (domain.SessionInterfaceTransition, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, "start:"+string(id))
	w.policies[id] = policy
	if w.onStart != nil {
		w.onStart()
	}
	if w.commitOnStart[id] {
		w.bound[id] = account
	}
	if _, ok := w.active[id]; ok {
		return domain.SessionInterfaceTransition{}, sessionmanager.ErrInterfaceTransitionInProgress
	}
	if err := w.startErr[id]; err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	phase, ok := w.initial[id]
	if !ok {
		phase = domain.SessionInterfaceTransitionTargetStarting
	}
	tid := "t-" + string(id)
	w.phase[tid] = phase
	active := 0
	for _, p := range w.phase {
		if p != domain.SessionInterfaceTransitionDraining && !p.Terminal() {
			active++
		}
	}
	if active > w.maxFlight {
		w.maxFlight = active
	}
	t := domain.SessionInterfaceTransition{ID: tid, SessionID: id, Phase: phase, TargetAccountID: account}
	if phase == domain.SessionInterfaceTransitionFailed {
		t.ErrorCode, t.ErrorDetail = "PREFLIGHT_FAILED", "login lost"
	}
	return t, nil
}
func (w *world) GetActiveSessionInterfaceTransition(_ context.Context, id domain.SessionID) (domain.SessionInterfaceTransition, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	t, ok := w.active[id]
	return t, ok, nil
}
func (w *world) GetSessionInterfaceTransition(_ context.Context, tid string) (domain.SessionInterfaceTransition, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	p, ok := w.phase[tid]
	return domain.SessionInterfaceTransition{ID: tid, Phase: p, ErrorCode: "PREFLIGHT_FAILED", ErrorDetail: "login lost"}, ok, nil
}

// settle moves a started session's transition to phase; completed binds it.
func (w *world) settle(id string, phase domain.SessionInterfaceTransitionPhase) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.phase["t-"+id] = phase
	if phase == domain.SessionInterfaceTransitionCompleted {
		w.bound[domain.SessionID(id)] = "acct-b"
	}
}

func (w *world) callLog() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.calls...)
}

func fixture(t *testing.T) (*Service, *world, *memAdoptions) {
	t.Helper()
	w, store := newWorld(), newMemAdoptions()
	svc := New(Config{Store: store, Accounts: w, Sessions: w, Mover: w, Poll: 2 * time.Millisecond, Slots: 2,
		NewID: func() func() string {
			n := 0
			var mu sync.Mutex
			return func() string { mu.Lock(); defer mu.Unlock(); n++; return fmt.Sprintf("batch-%d", n) }
		}()})
	t.Cleanup(func() {})
	return svc, w, store
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func states(b domain.AccountAdoption) map[string]domain.AccountAdoptionItemState {
	out := map[string]domain.AccountAdoptionItemState{}
	for _, it := range b.Items {
		out[string(it.SessionID)] = it.State
	}
	return out
}
