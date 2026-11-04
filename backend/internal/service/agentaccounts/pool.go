package agentaccounts

import (
	"context"
	"math/rand/v2"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Pool is the service as a provider-neutral ports.AccountPool. It is a
// separate type because Service.List already lists accounts with their readings.
func (s *Service) Pool() ports.AccountPool { return pool{s} }

type pool struct{ s *Service }

// SetPlacement sets how a new session without a chosen account picks one. The
// function is read at each spawn; nil or a default result keeps the provider's
// default account.
func (s *Service) SetPlacement(policy func() domain.AccountPlacement) {
	s.mu.Lock()
	s.placement = policy
	s.mu.Unlock()
}

func (s *Service) placementPolicy() domain.AccountPlacement {
	s.mu.Lock()
	f := s.placement
	s.mu.Unlock()
	if f == nil {
		return domain.AccountPlacementDefault
	}
	return f()
}

func (p pool) List(ctx context.Context, provider domain.AgentAccountProvider) ([]domain.AgentAccount, error) {
	return p.s.providerAccounts(ctx, provider)
}

func (p pool) Usable(ctx context.Context, provider domain.AgentAccountProvider) ([]domain.AgentAccount, error) {
	return p.s.usable(ctx, provider)
}

func (p pool) Limits(_ context.Context, accountID string) (domain.AgentAccountLimits, bool, error) {
	l, ok := p.s.cachedLimits(accountID)
	return l, ok, nil
}

func (p pool) Bind(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, accountID string) error {
	return p.s.BindSession(ctx, id, harness, accountID)
}

func (s *Service) providerAccounts(ctx context.Context, p domain.AgentAccountProvider) ([]domain.AgentAccount, error) {
	all, err := s.accounts(ctx)
	if err != nil {
		return nil, err
	}
	var out []domain.AgentAccount
	for _, a := range all {
		if a.Provider == p {
			out = append(out, a)
		}
	}
	return out, nil
}

// usable returns the provider's accounts that can launch as themselves: a
// subscription sign-in or an API key. Windows of every model count toward
// headroom and the at-limit skip, so an account whose Opus week is used up is
// skipped even for a Sonnet session.
func (s *Service) usable(ctx context.Context, p domain.AgentAccountProvider) ([]domain.AgentAccount, error) {
	all, err := s.providerAccounts(ctx, p)
	if err != nil {
		return nil, err
	}
	var out []domain.AgentAccount
	for _, a := range all {
		if s.beingRemoved(a.ID) {
			continue
		}
		r := s.cachedReading(a.ID)
		if r == nil || r.identityErr != "" || s.now().Sub(r.at) > freshFor {
			r = s.read(ctx, a)
		}
		if r != nil && r.identity.LoggedIn && !r.knownUnusable() {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *Service) cachedLimits(accountID string) (domain.AgentAccountLimits, bool) {
	r := s.cachedReading(accountID)
	if r == nil || r.limits == nil {
		return domain.AgentAccountLimits{}, false
	}
	return *r.limits, true
}

// place picks the account a new session of p starts on under the policy. ok is
// false when the policy is the default or no usable account is below its
// limits, and the caller keeps the default account.
func (s *Service) place(ctx context.Context, p domain.AgentAccountProvider) (domain.AgentAccount, bool, error) {
	policy := s.placementPolicy()
	if policy == domain.AccountPlacementDefault {
		return domain.AgentAccount{}, false, nil
	}
	usable, err := s.usable(ctx, p)
	if err != nil {
		return domain.AgentAccount{}, false, err
	}
	var open []domain.AgentAccount
	headroom := map[string]float64{}
	for _, a := range usable {
		h, known := 100.0, false
		if l, ok := s.cachedLimits(a.ID); ok {
			h, known = l.Headroom()
		}
		if known && h <= 0 {
			continue // at a limit
		}
		if !known {
			h = 0 // nothing read: usable, but ranks last by headroom
		}
		open = append(open, a)
		headroom[a.ID] = h
	}
	if len(open) == 0 {
		return domain.AgentAccount{}, false, nil
	}
	// cost is the primary rank, lower first; recent placements are the
	// secondary key and exact ties break at random, so a burst of spawns whose
	// readings have not moved yet still spreads across the accounts.
	cost := map[string]float64{}
	switch policy {
	case domain.AccountPlacementMostHeadroom:
		for _, a := range open {
			cost[a.ID] = -headroom[a.ID]
		}
	case domain.AccountPlacementLeastUsed:
		// A failed usage read ranks everyone equal rather than failing the spawn.
		rows, _ := s.store.AgentAccountUsageSince(ctx, s.now().Add(-24*time.Hour))
		for _, r := range rows {
			cost[r.AccountID] += float64(r.InputTokens + r.OutputTokens)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recentPlacements == nil {
		s.recentPlacements = map[string][]time.Time{}
	}
	now := s.now()
	var best []domain.AgentAccount
	for _, a := range open {
		s.pruneRecentLocked(a.ID, now)
		switch {
		case len(best) == 0:
		case cost[a.ID] < cost[best[0].ID] || (cost[a.ID] == cost[best[0].ID] && len(s.recentPlacements[a.ID]) < len(s.recentPlacements[best[0].ID])):
			best = best[:0]
		case cost[a.ID] == cost[best[0].ID] && len(s.recentPlacements[a.ID]) == len(s.recentPlacements[best[0].ID]):
		default:
			continue
		}
		best = append(best, a)
	}
	pick := best[rand.IntN(len(best))] //nolint:gosec // spreading load, not a secret
	s.recentPlacements[pick.ID] = append(s.recentPlacements[pick.ID], now)
	return pick, true, nil
}

// recentPlacementWindow is how long a placement counts against its account
// while the account's own readings have not caught up with the new session.
const recentPlacementWindow = 10 * time.Minute

func (s *Service) pruneRecentLocked(id string, now time.Time) {
	kept := s.recentPlacements[id][:0]
	for _, t := range s.recentPlacements[id] {
		if now.Sub(t) < recentPlacementWindow {
			kept = append(kept, t)
		}
	}
	s.recentPlacements[id] = kept
}
