package agentaccounts

import (
	"context"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// BindSession pins a session to accountID for its harness's provider, or to the
// provider's default when accountID is empty. Other harnesses have no accounts.
func (s *Service) BindSession(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, accountID string) error {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		if accountID != "" {
			return ports.ErrAgentAccountProviderMismatch
		}
		return nil
	}
	if accountID == "" {
		// Placement is an optimisation: a failed read never fails the spawn.
		placed, ok, err := s.place(ctx, p)
		if err != nil {
			s.log.Warn("account placement failed; using the default account", "provider", p, "error", err)
		}
		if ok {
			if err := s.prepare(s.providers[p], placed.Home); err != nil {
				s.log.Warn("placed account not launchable; using the default account", "account", placed.ID, "error", err)
			} else {
				return s.store.BindSessionAgentAccount(ctx, id, p, placed.ID, s.now())
			}
		}
		// A default known to be signed out would start the task signed out, or as
		// someone else: it starts on another signed-in account instead, and is
		// refused only when there is none.
		account, err := s.newSessionAccount(ctx, p)
		if err != nil {
			return err
		}
		if err := s.prepare(s.providers[p], account.Home); err != nil {
			return err
		}
		return s.store.BindSessionAgentAccount(ctx, id, p, account.ID, s.now())
	}
	account, err := s.pick(ctx, harness, accountID)
	if err != nil {
		return err
	}
	return s.store.BindSessionAgentAccount(ctx, id, p, account.ID, s.now())
}

// CommitSession records that the session now uses accountID, once its agent
// already runs there. The account was validated before the move; probing its
// CLI again here could only turn a live agent into a rollback.
func (s *Service) CommitSession(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, accountID string) error {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return ports.ErrAgentAccountProviderMismatch
	}
	account, err := s.find(ctx, accountID)
	if err != nil {
		return err
	}
	if account.Provider != p {
		return ports.ErrAgentAccountProviderMismatch
	}
	return s.store.BindSessionAgentAccount(ctx, id, p, account.ID, s.now())
}

// ValidateAccount checks that accountID can run harness now.
func (s *Service) ValidateAccount(ctx context.Context, harness domain.AgentHarness, accountID string) error {
	_, err := s.pick(ctx, harness, accountID)
	return err
}

// pick resolves an account chosen for harness. It must be of the harness's
// provider and signed in as itself, or the agent would start signed out or as
// someone else.
func (s *Service) pick(ctx context.Context, harness domain.AgentHarness, accountID string) (domain.AgentAccount, error) {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return domain.AgentAccount{}, ports.ErrAgentAccountProviderMismatch
	}
	account, err := s.find(ctx, accountID)
	if err != nil {
		return domain.AgentAccount{}, err
	}
	if s.beingRemoved(account.ID) {
		return domain.AgentAccount{}, ports.ErrAgentAccountUnknown
	}
	if account.Provider != p {
		return domain.AgentAccount{}, ports.ErrAgentAccountProviderMismatch
	}
	s.mu.Lock()
	r := s.readings[account.ID]
	s.mu.Unlock()
	// A failed reading may be transient (a timeout under load); read again
	// rather than refuse the account on it for the rest of the cache window.
	if r == nil || r.identityErr != "" || s.now().Sub(r.at) > freshFor {
		s.read(ctx, account)
		s.mu.Lock()
		r = s.readings[account.ID]
		s.mu.Unlock()
	}
	if r == nil || !r.identity.SignedIn {
		return domain.AgentAccount{}, ports.ErrAgentAccountSignedOut
	}
	if err := s.prepare(s.providers[p], account.Home); err != nil {
		return domain.AgentAccount{}, err
	}
	return account, nil
}

// prepare sets up an account folder for a launch. A folder that cannot link
// to the computer's history is refused: a session there would keep history of
// its own, and a move could not resume it. Other trouble is only logged.
func (s *Service) prepare(provider ports.AgentAccountProviderCLI, home string) error {
	if home == "" {
		return nil
	}
	err := provider.Prepare(home)
	if errors.Is(err, ports.ErrAgentAccountSplitHistory) || errors.Is(err, ports.ErrAgentAccountUnshared) {
		return err
	}
	if err != nil {
		s.log.Warn("prepare account folder", "home", home, "error", err)
	}
	return nil
}

// SessionAccountID is the account a session uses for harness's provider, or
// "" when the harness has no accounts.
func (s *Service) SessionAccountID(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) (string, error) {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return "", nil
	}
	account, _, err := s.SessionAccount(ctx, id, p)
	return account.ID, err
}

// SessionLimits reads only the session's pinned account. A refresh bypasses the
// display cache; a binding or identity change never falls back to the default.
func (s *Service) SessionLimits(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, refresh bool) (string, domain.AgentAccountLimits, error) {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return "", domain.AgentAccountLimits{}, ports.ErrAgentAccountProviderMismatch
	}
	account, err := s.boundAccount(ctx, id, p)
	if err != nil {
		return "", domain.AgentAccountLimits{}, err
	}
	s.mu.Lock()
	r := s.readings[account.ID]
	s.mu.Unlock()
	if refresh || r == nil || s.now().Sub(r.at) > freshFor {
		r = s.read(ctx, account)
	}
	if err := ctx.Err(); err != nil {
		return account.ID, domain.AgentAccountLimits{}, err
	}
	current, err := s.boundAccount(ctx, id, p)
	if err != nil {
		return account.ID, domain.AgentAccountLimits{}, err
	}
	if current.ID != account.ID || current.Home != account.Home {
		return account.ID, domain.AgentAccountLimits{}, ports.ErrAgentAccountChanged
	}
	if r != nil && r.mismatch {
		return account.ID, domain.AgentAccountLimits{}, ports.ErrAgentAccountChanged
	}
	if r == nil {
		return account.ID, domain.AgentAccountLimits{}, errors.New("account limits are unavailable")
	}
	if r.identityErr != "" {
		return account.ID, domain.AgentAccountLimits{}, fmt.Errorf("read account identity: %s", r.identityErr)
	}
	if !r.identity.SignedIn {
		return account.ID, domain.AgentAccountLimits{}, ports.ErrAgentAccountSignedOut
	}
	if r.limitsErr != "" {
		return account.ID, domain.AgentAccountLimits{}, fmt.Errorf("read account limits: %s", r.limitsErr)
	}
	if r.limits == nil {
		return account.ID, domain.AgentAccountLimits{}, errors.New("account limits are unavailable")
	}
	return account.ID, *r.limits, nil
}

func (s *Service) boundAccount(ctx context.Context, id domain.SessionID, p domain.AgentAccountProvider) (domain.AgentAccount, error) {
	accountID, bound, err := s.store.GetSessionAgentAccount(ctx, id, p)
	if err != nil {
		return domain.AgentAccount{}, err
	}
	if !bound || s.beingRemoved(accountID) {
		return domain.AgentAccount{}, ports.ErrAgentAccountChanged
	}
	account, err := s.find(ctx, accountID)
	if errors.Is(err, ports.ErrAgentAccountUnknown) || (err == nil && account.Provider != p) {
		return domain.AgentAccount{}, ports.ErrAgentAccountChanged
	}
	return account, err
}

// SessionAccount is the account a session uses for the provider now: its
// binding, or the provider's default when it has none or its account is gone.
func (s *Service) SessionAccount(ctx context.Context, id domain.SessionID, p domain.AgentAccountProvider) (domain.AgentAccount, bool, error) {
	accountID, bound, err := s.store.GetSessionAgentAccount(ctx, id, p)
	if err != nil {
		return domain.AgentAccount{}, false, err
	}
	if bound {
		if account, err := s.find(ctx, accountID); err == nil {
			return account, true, nil
		}
	}
	def, err := s.defaultID(ctx, p)
	if err != nil {
		return domain.AgentAccount{}, false, err
	}
	account, err := s.find(ctx, def)
	return account, false, err
}

// SessionAccounts is the account a session uses for each provider.
func (s *Service) SessionAccounts(ctx context.Context, id domain.SessionID) (map[domain.AgentAccountProvider]string, error) {
	out := map[domain.AgentAccountProvider]string{}
	for _, p := range domain.AgentAccountProviders {
		if s.providers[p] == nil {
			continue
		}
		account, _, err := s.SessionAccount(ctx, id, p)
		if err != nil {
			return nil, err
		}
		out[p] = account.ID
	}
	return out, nil
}

// SessionLaunchEnv is the environment that runs each provider's CLI under the
// session's account. A session with no account for a provider yet is bound to
// that provider's default here, so a later default change does not move it.
func (s *Service) SessionLaunchEnv(ctx context.Context, id domain.SessionID) (map[string]string, error) {
	return s.launchEnv(ctx, id, domain.AgentAccount{})
}

// SessionLaunchEnvWith is SessionLaunchEnv with accountID in place of the
// session's account for its provider, for starting an agent on an account the
// session moves to. The session's binding is not changed.
func (s *Service) SessionLaunchEnvWith(ctx context.Context, id domain.SessionID, accountID string) (map[string]string, error) {
	account, err := s.find(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if s.beingRemoved(account.ID) {
		return nil, ports.ErrAgentAccountUnknown
	}
	return s.launchEnv(ctx, id, account)
}

func (s *Service) launchEnv(ctx context.Context, id domain.SessionID, override domain.AgentAccount) (map[string]string, error) {
	env := map[string]string{}
	var errs []error
	for _, p := range domain.AgentAccountProviders {
		providerEnv, err := s.launchEnvOne(ctx, id, p, override)
		if err != nil {
			errs = append(errs, err)
		}
		for key, value := range providerEnv {
			env[key] = value
		}
	}
	return env, errors.Join(errs...)
}
