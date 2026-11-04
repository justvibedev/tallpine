package agentaccounts

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// DefaultSignIn reports the sign-in of the saved account new sessions of
// harness start on, named by the email it was added with; handled is false when
// they start on the computer's own login, whose readiness the CLI reports
// itself. A reading older than the cache window is read again, so readiness
// polling costs one CLI call per window.
func (s *Service) DefaultSignIn(ctx context.Context, harness domain.AgentHarness) (identity domain.AgentAccountIdentityCheck, handled bool) {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return identity, false
	}
	def, err := s.defaultID(ctx, p)
	if err != nil || def == domain.DeviceAgentAccountID(p) {
		return identity, false
	}
	account, err := s.find(ctx, def)
	if err != nil {
		return identity, false
	}
	s.mu.Lock()
	r := s.readings[account.ID]
	s.mu.Unlock()
	if r == nil || s.now().Sub(r.at) > freshFor {
		s.read(ctx, account)
		s.mu.Lock()
		r = s.readings[account.ID]
		s.mu.Unlock()
	}
	if r == nil || (r.identityErr != "" && !r.mismatch) {
		return identity, false // not known: leave it to the CLI's own readiness
	}
	if !r.identity.SignedIn {
		// New sessions start on another signed-in account instead, if there is one.
		next, err := s.newSessionAccount(ctx, p)
		switch {
		case err == nil && next.Device():
			return identity, false
		case err == nil:
			account = next
			if r = s.cachedReading(next.ID); r == nil {
				return identity, false
			}
		}
	}
	identity = r.identity
	// A mismatched folder names another login; the account is still its own.
	if account.Email != "" {
		identity.Email = account.Email
	}
	return identity, true
}

// SetDefaultChanged late-binds what must hear that the account new sessions of
// a provider start on, or its sign-in, may have changed (agent readiness).
func (s *Service) SetDefaultChanged(changed func(domain.AgentAccountProvider)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultChanged = changed
}

func (s *Service) notifyDefaultChanged(p domain.AgentAccountProvider) {
	s.mu.Lock()
	changed := s.defaultChanged
	s.mu.Unlock()
	if changed != nil {
		changed(p)
	}
}

// RunsOnComputerLogin reports whether a new session of harness asking for
// accountID ("" for the default) would launch on the computer's own login.
// Harnesses without accounts always do.
func (s *Service) RunsOnComputerLogin(ctx context.Context, harness domain.AgentHarness, accountID string) bool {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return true
	}
	if accountID == "" {
		account, err := s.newSessionAccount(ctx, p)
		if err != nil {
			return true
		}
		accountID = account.ID
	}
	return accountID == domain.DeviceAgentAccountID(p)
}

// newSessionAccount is the account a new session of p starts on: the default,
// or, when the default is known to be signed out or to hold another login, the
// first other account that is signed in. The session's account shows which.
// Only a bad default costs CLI calls; a good one is answered from the cache.
func (s *Service) newSessionAccount(ctx context.Context, p domain.AgentAccountProvider) (domain.AgentAccount, error) {
	def, err := s.defaultID(ctx, p)
	if err != nil {
		return domain.AgentAccount{}, err
	}
	account, err := s.find(ctx, def)
	if err != nil {
		return domain.AgentAccount{}, err
	}
	r := s.cachedReading(account.ID)
	if !r.knownUnusable() {
		return account, nil
	}
	// The user may have signed it in outside Tallpine since an old reading.
	if s.now().Sub(r.at) > freshFor {
		if r = s.read(ctx, account); !r.knownUnusable() {
			return account, nil
		}
	}
	accounts, err := s.accounts(ctx)
	if err != nil {
		return domain.AgentAccount{}, err
	}
	for _, other := range accounts {
		if other.Provider != p || other.ID == account.ID || s.beingRemoved(other.ID) {
			continue
		}
		r := s.cachedReading(other.ID)
		if r == nil || r.identityErr != "" || s.now().Sub(r.at) > freshFor {
			r = s.read(ctx, other)
		}
		if r != nil && r.identity.SignedIn {
			return other, nil
		}
	}
	return domain.AgentAccount{}, ports.ErrAgentAccountDefaultUnusable
}

func (s *Service) cachedReading(id string) *reading {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readings[id]
}

// RecheckSessionAccount reads the account a session runs on again, after its
// agent was refused for its credentials. Claude clears a folder's login when
// its refresh token is refused, so the account reads as signed out at once and
// new sessions stop starting on it, instead of after the cache window.
//
// Requests for one account are throttled: the first reads at once, and any that
// arrive while it runs or within recheckEvery of it share one later read, so a
// burst of refused sessions costs two reads, not one per session.
func (s *Service) RecheckSessionAccount(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return
	}
	account, _, err := s.SessionAccount(ctx, id, p)
	if err != nil {
		s.log.Debug("recheck session account", "session", id, "error", err)
		return
	}
	s.recheck(ctx, account)
}

func (s *Service) recheck(ctx context.Context, account domain.AgentAccount) {
	s.mu.Lock()
	st := s.rechecks[account.ID]
	if st == nil {
		st = &recheckState{}
		s.rechecks[account.ID] = st
	}
	if st.running {
		st.pending = true
		s.mu.Unlock()
		return
	}
	st.running = true
	for {
		wait := s.recheckEvery - time.Since(st.last)
		s.mu.Unlock()
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				s.mu.Lock()
				st.running, st.pending = false, false
				s.mu.Unlock()
				return
			}
		}
		s.mu.Lock()
		st.last, st.pending = time.Now(), false
		s.mu.Unlock()
		s.read(ctx, account)
		s.mu.Lock()
		if !st.pending {
			st.running = false
			s.mu.Unlock()
			return
		}
	}
}

// SetDefault makes accountID the account new sessions of its provider start on.
// Running sessions and the computer's own CLI are not touched.
func (s *Service) SetDefault(ctx context.Context, accountID string) error {
	account, err := s.find(ctx, accountID)
	if err != nil {
		return err
	}
	if err := s.store.SetAgentAccountDefault(ctx, account.Provider, account.ID); err != nil {
		return err
	}
	s.notifyDefaultChanged(account.Provider)
	return nil
}
