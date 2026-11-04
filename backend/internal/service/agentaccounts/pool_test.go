package agentaccounts

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// limitClaude is fakeClaude with limits per account folder.
type limitClaude struct {
	*fakeClaude
	byHome map[string][]domain.AgentAccountLimitWindow
}

func (f *limitClaude) Limits(_ context.Context, home string) (domain.AgentAccountLimits, error) {
	return domain.AgentAccountLimits{Windows: f.byHome[home]}, nil
}

type usageStore struct {
	*memStore
	usage []domain.AgentAccountUsage
}

func (s *usageStore) AgentAccountUsageSince(context.Context, time.Time) ([]domain.AgentAccountUsage, error) {
	return s.usage, nil
}

func win(used float64) []domain.AgentAccountLimitWindow {
	return []domain.AgentAccountLimitWindow{{ID: "five_hour", UsedPercent: used}}
}

// poolFixture has the device account plus saved accounts a1 and a2, all signed
// in, with the given used percent per account.
func poolFixture(t *testing.T, policy domain.AccountPlacement, used map[string]float64) (*Service, *usageStore) {
	t.Helper()
	store := &usageStore{memStore: &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}}
	claude := &limitClaude{
		fakeClaude: &fakeClaude{signedInAs: map[string]string{"": "d@x.com", "/h/a1": "a1@x.com", "/h/a2": "a2@x.com"}},
		byHome:     map[string][]domain.AgentAccountLimitWindow{},
	}
	for id, home := range map[string]string{"device-claude": "", "a1": "/h/a1", "a2": "/h/a2"} {
		if u, ok := used[id]; ok {
			claude.byHome[home] = win(u)
		}
	}
	store.accounts = append(store.accounts,
		domain.AgentAccount{ID: "a1", Provider: domain.AgentAccountProviderClaude, Home: "/h/a1", Email: "a1@x.com"},
		domain.AgentAccount{ID: "a2", Provider: domain.AgentAccountProviderClaude, Home: "/h/a2", Email: "a2@x.com"})
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, t.TempDir(), fakeTerminals{}, nil)
	svc.SetPlacement(func() domain.AccountPlacement { return policy })
	return svc, store
}

func bound(t *testing.T, svc *Service, store *usageStore) string {
	t.Helper()
	if err := svc.Pool().Bind(context.Background(), "s1", domain.HarnessClaudeCode, ""); err != nil {
		t.Fatal(err)
	}
	return store.bindings["s1"]
}

func TestPlacementDefaultKeepsDefaultAccount(t *testing.T) {
	svc, store := poolFixture(t, domain.AccountPlacementDefault, map[string]float64{"device-claude": 99, "a1": 0, "a2": 0})
	if got := bound(t, svc, store); got != "device-claude" {
		t.Fatalf("bound %q, want the default device account", got)
	}
}

func TestPlacementMostHeadroomPicksEmptiest(t *testing.T) {
	svc, store := poolFixture(t, domain.AccountPlacementMostHeadroom, map[string]float64{"device-claude": 80, "a1": 40, "a2": 10})
	if got := bound(t, svc, store); got != "a2" {
		t.Fatalf("bound %q, want a2", got)
	}
}

func TestPlacementSkipsLimitedAndSignedOut(t *testing.T) {
	for _, policy := range []domain.AccountPlacement{domain.AccountPlacementRandom, domain.AccountPlacementMostHeadroom, domain.AccountPlacementLeastUsed} {
		svc, store := poolFixture(t, policy, map[string]float64{"device-claude": 100, "a1": 100, "a2": 60})
		if got := bound(t, svc, store); got != "a2" {
			t.Fatalf("%s: bound %q, want the only open account a2", policy, got)
		}
		svc, store = poolFixture(t, policy, map[string]float64{"device-claude": 100, "a1": 5, "a2": 5})
		delete(svc.providers[domain.AgentAccountProviderClaude].(*limitClaude).signedInAs, "/h/a1")
		delete(svc.providers[domain.AgentAccountProviderClaude].(*limitClaude).signedInAs, "")
		if got := bound(t, svc, store); got != "a2" {
			t.Fatalf("%s: bound %q, want signed-in a2", policy, got)
		}
	}
}

func TestPlacementLeastUsedPicksFewestTokens(t *testing.T) {
	svc, store := poolFixture(t, domain.AccountPlacementLeastUsed, map[string]float64{"device-claude": 10, "a1": 10, "a2": 10})
	store.usage = []domain.AgentAccountUsage{
		{AccountID: "device-claude", InputTokens: 500},
		{AccountID: "a1", InputTokens: 50, OutputTokens: 10},
		{AccountID: "a2", InputTokens: 300},
	}
	if got := bound(t, svc, store); got != "a1" {
		t.Fatalf("bound %q, want a1", got)
	}
}

func TestPlacementRandomReachesEveryOpenAccount(t *testing.T) {
	svc, store := poolFixture(t, domain.AccountPlacementRandom, map[string]float64{"device-claude": 10, "a1": 10, "a2": 10})
	seen := map[string]bool{}
	for range 200 {
		seen[bound(t, svc, store)] = true
	}
	if len(seen) != 3 {
		t.Fatalf("random reached %v, want all three", seen)
	}
}

func TestPlacementWithOneAccountEveryPolicyPicksIt(t *testing.T) {
	for _, policy := range []domain.AccountPlacement{domain.AccountPlacementDefault, domain.AccountPlacementRandom, domain.AccountPlacementMostHeadroom, domain.AccountPlacementLeastUsed} {
		svc, store := poolFixture(t, policy, map[string]float64{"device-claude": 30})
		store.accounts = nil
		if got := bound(t, svc, store); got != "device-claude" {
			t.Fatalf("%s: bound %q", policy, got)
		}
	}
}

func TestPlacementAllLimitedFallsBackToDefault(t *testing.T) {
	svc, store := poolFixture(t, domain.AccountPlacementMostHeadroom, map[string]float64{"device-claude": 100, "a1": 100, "a2": 100})
	if got := bound(t, svc, store); got != "device-claude" {
		t.Fatalf("bound %q, want the default when every account is limited", got)
	}
}

func TestExplicitAccountIgnoresPlacement(t *testing.T) {
	svc, store := poolFixture(t, domain.AccountPlacementMostHeadroom, map[string]float64{"device-claude": 0, "a1": 90, "a2": 0})
	if err := svc.Pool().Bind(context.Background(), "s1", domain.HarnessClaudeCode, "a1"); err != nil {
		t.Fatal(err)
	}
	if got := store.bindings["s1"]; got != "a1" {
		t.Fatalf("bound %q, want the explicit a1", got)
	}
}

func TestPoolListUsableLimits(t *testing.T) {
	svc, _ := poolFixture(t, domain.AccountPlacementDefault, map[string]float64{"a1": 42})
	ctx := context.Background()
	if all, _ := svc.Pool().List(ctx, domain.AgentAccountProviderClaude); len(all) != 3 {
		t.Fatalf("List = %d accounts", len(all))
	}
	if usable, _ := svc.Pool().Usable(ctx, domain.AgentAccountProviderClaude); len(usable) != 3 {
		t.Fatalf("Usable = %d accounts", len(usable))
	}
	l, ok, _ := svc.Pool().Limits(ctx, "a1")
	if !ok || len(l.Windows) != 1 || l.Windows[0].UsedPercent != 42 {
		t.Fatalf("Limits = %+v ok=%v", l, ok)
	}
}

func TestPlacementBurstSpreadsAcrossEqualAccounts(t *testing.T) {
	for _, policy := range []domain.AccountPlacement{domain.AccountPlacementMostHeadroom, domain.AccountPlacementLeastUsed} {
		svc, store := poolFixture(t, policy, map[string]float64{"device-claude": 10, "a1": 10, "a2": 10})
		counts := map[string]int{}
		for i := range 30 {
			id := domain.SessionID("s" + string(rune('a'+i)))
			if err := svc.Pool().Bind(context.Background(), id, domain.HarnessClaudeCode, ""); err != nil {
				t.Fatal(err)
			}
			counts[store.bindings[id]]++
		}
		for _, id := range []string{"device-claude", "a1", "a2"} {
			if counts[id] != 10 {
				t.Fatalf("%s: burst spread %v, want 10 each", policy, counts)
			}
		}
	}
}

func TestPlacementReachesAPIKeyDeviceAccount(t *testing.T) {
	svc, store := poolFixture(t, domain.AccountPlacementRandom, map[string]float64{})
	claude := svc.providers[domain.AgentAccountProviderClaude].(*limitClaude)
	delete(claude.signedInAs, "")
	claude.apiKey = map[string]bool{"": true}
	// Named a1 and a2 stay signed in, so a pool that skipped the API-key
	// device account would still place every session, just never on it.
	seen := map[string]bool{}
	for range 100 {
		seen[bound(t, svc, store)] = true
	}
	if !seen["device-claude"] {
		t.Fatalf("random never chose the API-key device account: %v", seen)
	}
}

type failingUsageStore struct{ *usageStore }

func (failingUsageStore) AgentAccountUsageSince(context.Context, time.Time) ([]domain.AgentAccountUsage, error) {
	return nil, errors.New("usage unreadable")
}

func TestFailedUsageReadNeverFailsASpawn(t *testing.T) {
	svc, store := poolFixture(t, domain.AccountPlacementLeastUsed, map[string]float64{"device-claude": 10, "a1": 10, "a2": 10})
	svc.store = failingUsageStore{store}
	if err := svc.Pool().Bind(context.Background(), "s1", domain.HarnessClaudeCode, ""); err != nil {
		t.Fatalf("spawn failed on an unreadable usage table: %v", err)
	}
	if store.bindings["s1"] == "" {
		t.Fatal("session not bound")
	}
}

// flakyListStore fails its first account listing, which placement makes
// before the default path lists them again.
type flakyListStore struct {
	*usageStore
	failed bool
}

func (s *flakyListStore) ListAgentAccounts(ctx context.Context) ([]domain.AgentAccount, error) {
	if !s.failed {
		s.failed = true
		return nil, errors.New("accounts unreadable")
	}
	return s.usageStore.ListAgentAccounts(ctx)
}

func TestFailedPlacementReadFallsBackToTheDefault(t *testing.T) {
	svc, store := poolFixture(t, domain.AccountPlacementRandom, map[string]float64{"device-claude": 10, "a1": 10, "a2": 10})
	svc.store = &flakyListStore{usageStore: store}
	if err := svc.Pool().Bind(context.Background(), "s1", domain.HarnessClaudeCode, ""); err != nil {
		t.Fatalf("spawn failed on a failed placement read: %v", err)
	}
	if got := store.bindings["s1"]; got != "device-claude" {
		t.Fatalf("bound %q, want the default", got)
	}
}
