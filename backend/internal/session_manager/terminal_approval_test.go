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

// cursorRuntime models a TUI list: a Down key moves the cursor, Enter records
// the row it was on.
type cursorRuntime struct {
	*fakeRuntime
	mu        sync.Mutex
	cursor    int
	keys      []string
	entered   int
	enteredAt []int
	stuck     bool
}

func (r *cursorRuntime) GetOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	r.mu.Lock()
	out := "dialog cursor=" + string(rune('0'+r.cursor))
	r.mu.Unlock()
	// A screen capture takes time; a stale capture is what makes unserialized
	// answers send their keys twice.
	time.Sleep(20 * time.Millisecond)
	return out, nil
}

func (r *cursorRuntime) SendInput(_ context.Context, _ ports.RuntimeHandle, in string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys = append(r.keys, in)
	switch in {
	case terminalKeyUp:
		if r.cursor > 0 {
			r.cursor--
		}
	case terminalKeyDown:
		if !r.stuck {
			r.cursor++
		}
	case terminalKeyEnter:
		r.entered = r.cursor
		r.enteredAt = append(r.enteredAt, r.cursor)
	}
	return nil
}

type approvalAgent struct{ fakeAgent }

func (approvalAgent) ParseTerminalApproval(out string) (ports.TerminalApproval, bool) {
	if !strings.HasPrefix(out, "dialog cursor=") {
		return ports.TerminalApproval{}, false
	}
	cur := int(out[len("dialog cursor=")] - '0')
	ids := []ports.ChatDecisionKind{ports.ChatDecisionAllowOnce, ports.ChatDecisionRejectOnce}
	a := ports.TerminalApproval{Summary: "shell requires approval", Command: "shell → touch x"}
	for i, k := range ids {
		a.Options = append(a.Options, ports.TerminalApprovalOption{ID: string(k), Kind: k, Index: i, Selected: i == cur})
	}
	return a, true
}

type approvalAgents struct{}

func (approvalAgents) Agent(domain.AgentHarness) (ports.Agent, bool) { return approvalAgent{}, true }

func newApprovalManager(t *testing.T) (*Manager, *cursorRuntime, domain.SessionID) {
	t.Helper()
	m, st, rt, _ := newManager()
	cr := &cursorRuntime{fakeRuntime: rt}
	m.runtime = cr
	m.agents = approvalAgents{}
	rec, err := st.CreateSession(context.Background(), domain.SessionRecord{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessKiro, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{RuntimeHandleID: "h1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return m, cr, rec.ID
}

func TestResolveTerminalApprovalMovesCursorThenEnter(t *testing.T) {
	m, rt, id := newApprovalManager(t)
	pending, supported, err := m.PendingTerminalApprovals(context.Background(), id)
	if err != nil || !supported || len(pending) != 1 {
		t.Fatalf("pending = %v %v %v", pending, supported, err)
	}
	if err := m.ResolveTerminalApproval(context.Background(), id, pending[0].RequestID, string(ports.ChatDecisionRejectOnce)); err != nil {
		t.Fatal(err)
	}
	if rt.entered != 1 || len(rt.keys) != 2 {
		t.Fatalf("entered=%d keys=%q, want deny row via one Down then Enter", rt.entered, rt.keys)
	}
}

func TestResolveTerminalApprovalRefusesWhenCursorDoesNotMove(t *testing.T) {
	m, rt, id := newApprovalManager(t)
	rt.stuck = true
	pending, _, _ := m.PendingTerminalApprovals(context.Background(), id)
	err := m.ResolveTerminalApproval(context.Background(), id, pending[0].RequestID, string(ports.ChatDecisionRejectOnce))
	if err == nil {
		t.Fatal("want error, Enter would have answered the wrong row")
	}
	for _, k := range rt.keys {
		if k == terminalKeyEnter {
			t.Fatal("Enter sent although cursor was on the wrong row")
		}
	}
}

func TestResolveTerminalApprovalStaleRequest(t *testing.T) {
	m, _, id := newApprovalManager(t)
	err := m.ResolveTerminalApproval(context.Background(), id, "term-other", string(ports.ChatDecisionAllowOnce))
	if !errors.Is(err, ports.ErrTerminalApprovalNotPending) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveTerminalApprovalMovesUpWhenCursorIsBelowTarget(t *testing.T) {
	m, rt, id := newApprovalManager(t)
	rt.cursor = 1
	pending, _, _ := m.PendingTerminalApprovals(context.Background(), id)
	if err := m.ResolveTerminalApproval(context.Background(), id, pending[0].RequestID, string(ports.ChatDecisionAllowOnce)); err != nil {
		t.Fatal(err)
	}
	if rt.entered != 0 || rt.keys[0] != terminalKeyUp {
		t.Fatalf("entered=%d keys=%q, want Up then Enter on allow row", rt.entered, rt.keys)
	}
}

func TestResolveTerminalApprovalSerializesConcurrentAnswers(t *testing.T) {
	m, rt, id := newApprovalManager(t)
	pending, _, _ := m.PendingTerminalApprovals(context.Background(), id)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.ResolveTerminalApproval(context.Background(), id, pending[0].RequestID, string(ports.ChatDecisionRejectOnce))
		}()
	}
	wg.Wait()
	downs := 0
	for _, k := range rt.keys {
		if k == terminalKeyDown {
			downs++
		}
	}
	for _, row := range rt.enteredAt {
		if row != 1 {
			t.Fatalf("Enter pressed on row %d, want the confirmed row 1 (keys %q)", row, rt.keys)
		}
	}
	if downs != 1 {
		t.Fatalf("downs=%d keys=%q, want the second answer to find the cursor already on row 1", downs, rt.keys)
	}
}
