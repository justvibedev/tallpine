package accountfailover

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type fakePool struct {
	ports.AccountPool
	usable    []domain.AgentAccount
	usableErr error
	used      map[string]float64 // used percent; missing means no reading
	limitsErr error
}

func (p *fakePool) Usable(context.Context, domain.AgentAccountProvider) ([]domain.AgentAccount, error) {
	return p.usable, p.usableErr
}

func (p *fakePool) Limits(_ context.Context, id string) (domain.AgentAccountLimits, bool, error) {
	if p.limitsErr != nil {
		return domain.AgentAccountLimits{}, false, p.limitsErr
	}
	u, ok := p.used[id]
	if !ok {
		return domain.AgentAccountLimits{}, false, nil
	}
	return domain.AgentAccountLimits{Windows: []domain.AgentAccountLimitWindow{{ID: "w", UsedPercent: u}}}, true, nil
}

type fakeSessions struct{ account string }

func (s *fakeSessions) SessionAccountID(context.Context, domain.SessionID, domain.AgentHarness) (string, error) {
	return s.account, nil
}

type fakeMover struct {
	targets []string
	err     error
	sess    *fakeSessions
}

func (m *fakeMover) StartAccountTransition(_ context.Context, _ domain.SessionID, id string, _ domain.SessionInterfaceTransitionPolicy) (domain.SessionInterfaceTransition, error) {
	if m.err != nil {
		return domain.SessionInterfaceTransition{}, m.err
	}
	m.targets = append(m.targets, id)
	m.sess.account = id
	return domain.SessionInterfaceTransition{ID: "t-" + id}, nil
}

func acct(ids ...string) []domain.AgentAccount {
	var out []domain.AgentAccount
	for _, id := range ids {
		out = append(out, domain.AgentAccount{ID: id, Provider: domain.AgentAccountProviderClaude})
	}
	return out
}

type fixture struct {
	f    *Failover
	pool *fakePool
	mov  *fakeMover
	now  time.Time
}

func newFixture(used map[string]float64, ids ...string) *fixture {
	fx := &fixture{now: time.Unix(1000, 0)}
	sess := &fakeSessions{account: "a"}
	fx.pool = &fakePool{usable: acct(ids...), used: used}
	fx.mov = &fakeMover{sess: sess}
	fx.f = New(Config{Pool: fx.pool, Sessions: sess, Mover: fx.mov, Now: func() time.Time { return fx.now }})
	return fx
}

func (fx *fixture) try() string {
	return fx.f.Failover(context.Background(), "s1", domain.HarnessClaudeCode)
}

func TestFailoverPicksMostHeadroomAndSkipsLimited(t *testing.T) {
	fx := newFixture(map[string]float64{"a": 100, "b": 100, "c": 40, "d": 10, "e": 60}, "a", "b", "c", "d", "e")
	if id := fx.try(); id != "t-d" {
		t.Fatalf("move = %q, want t-d (10%% used, not the limited b)", id)
	}
}

func TestFailoverWaitsWhenEveryOtherAccountIsLimitedOrUnread(t *testing.T) {
	fx := newFixture(map[string]float64{"a": 100, "b": 100}, "a", "b", "c") // c has no reading
	if id := fx.try(); id != "" || len(fx.mov.targets) != 0 {
		t.Fatalf("moved to %q with no account showing headroom", id)
	}
}

func TestFailoverCooldownHoldsEvenWithRoomElsewhere(t *testing.T) {
	fx := newFixture(map[string]float64{"a": 100, "b": 10, "c": 50}, "a", "b", "c")
	if id := fx.try(); id != "t-b" {
		t.Fatalf("first move = %q, want t-b", id)
	}
	fx.pool.used["b"] = 100 // the new account is limited too
	fx.now = fx.now.Add(5 * time.Minute)
	if fx.try() != "" || len(fx.mov.targets) != 1 {
		t.Fatalf("moved again inside the cooldown: %v", fx.mov.targets)
	}
}

func TestFailoverMayReturnToTheFirstAccountAfterTheCooldown(t *testing.T) {
	fx := newFixture(map[string]float64{"a": 0, "b": 0}, "a", "b")
	if fx.try() != "t-b" {
		t.Fatal("first move not started")
	}
	// b hits its limit, a has reset: refused inside the cooldown, allowed after.
	fx.pool.used["b"], fx.pool.used["a"] = 100, 0
	fx.now = fx.now.Add(DefaultCooldown - time.Minute)
	if id := fx.try(); id != "" {
		t.Fatalf("moved back inside the cooldown: %q", id)
	}
	fx.now = fx.now.Add(2 * time.Minute)
	if id := fx.try(); id != "t-a" {
		t.Fatalf("after the cooldown move = %q, want back to t-a", id)
	}
}

func TestFailedReadsNeverFailAndWaitForReset(t *testing.T) {
	fx := newFixture(map[string]float64{"b": 0}, "a", "b")
	fx.pool.usableErr = errors.New("accounts unreadable")
	if id := fx.try(); id != "" {
		t.Fatalf("moved despite unreadable accounts: %q", id)
	}
	fx.now = fx.now.Add(2 * DefaultRetryAfter)
	fx.pool.usableErr, fx.pool.limitsErr = nil, errors.New("limits unreadable")
	if id := fx.try(); id != "" {
		t.Fatalf("moved despite unreadable limits: %q", id)
	}
	fx.now = fx.now.Add(2 * DefaultRetryAfter)
	fx.pool.limitsErr = nil
	fx.mov.err = errors.New("preflight refused")
	if id := fx.try(); id != "" {
		t.Fatalf("reported a move the manager refused: %q", id)
	}
	fx.now = fx.now.Add(2 * DefaultRetryAfter)
	fx.mov.err = nil
	if fx.try() == "" {
		t.Fatal("failover did not recover once reads and the move worked")
	}
}

func TestFailoverRetriesNoCandidateOnlyAfterRetryInterval(t *testing.T) {
	fx := newFixture(map[string]float64{"a": 100, "b": 100}, "a", "b")
	fx.try()
	fx.pool.used["b"] = 0
	if fx.try() != "" {
		t.Fatal("retried before the retry interval")
	}
	fx.now = fx.now.Add(DefaultRetryAfter)
	if fx.try() == "" {
		t.Fatal("did not retry after the interval")
	}
}

func TestFailoverDoesNothingWhileDisabled(t *testing.T) {
	fx := newFixture(map[string]float64{"a": 100, "b": 0}, "a", "b")
	on := false
	fx.f.cfg.Enabled = func() bool { return on }
	if id := fx.try(); id != "" || len(fx.mov.targets) != 0 {
		t.Fatalf("moved while disabled: %q", id)
	}
	on = true
	if id := fx.try(); id != "t-b" {
		t.Fatalf("after enabling, move = %q, want t-b (a disabled attempt must not burn the retry interval)", id)
	}
}
