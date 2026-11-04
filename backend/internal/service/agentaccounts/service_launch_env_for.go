package agentaccounts

import (
	"context"
	"errors"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

var _ ports.AgentAccountProviderEnv = (*Service)(nil)

// NewSessionLaunchEnv returns the default/fallback account environment a new
// session of harness can use without binding a session. Model discovery uses it
// so the provider catalog is read under a working launch account.
func (s *Service) NewSessionLaunchEnv(ctx context.Context, harness domain.AgentHarness) (map[string]string, error) {
	return s.newSessionLaunchEnv(ctx, harness, true)
}

// NewSessionLaunchEnvReadOnly is NewSessionLaunchEnv without preparing the
// account folder, for read paths such as catalog fingerprinting.
func (s *Service) NewSessionLaunchEnvReadOnly(ctx context.Context, harness domain.AgentHarness) (map[string]string, error) {
	return s.newSessionLaunchEnv(ctx, harness, false)
}

func (s *Service) newSessionLaunchEnv(ctx context.Context, harness domain.AgentHarness, prepare bool) (map[string]string, error) {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return nil, nil
	}
	account, err := s.newSessionAccount(ctx, p)
	if err != nil {
		return nil, err
	}
	provider := s.providers[p]
	if account.Device() {
		return provider.LaunchEnv(""), nil
	}
	if prepare {
		if err := s.prepare(provider, account.Home); err != nil {
			return provider.LaunchEnv(account.Home), err
		}
	}
	return provider.LaunchEnv(account.Home), nil
}

// SessionLaunchEnvFor is SessionLaunchEnv limited to one provider. Binding the
// default, falling back from a removed account and linking the account's shared
// history all happen for that provider only.
func (s *Service) SessionLaunchEnvFor(ctx context.Context, id domain.SessionID, provider domain.AgentAccountProvider) (map[string]string, error) {
	return s.launchEnvOne(ctx, id, provider, domain.AgentAccount{})
}

// launchEnvOne is one provider's share of launchEnv: the env that runs its CLI
// under the session's account, with the error of that provider alone.
func (s *Service) launchEnvOne(ctx context.Context, id domain.SessionID, p domain.AgentAccountProvider, override domain.AgentAccount) (map[string]string, error) {
	provider := s.providers[p]
	if provider == nil {
		return nil, nil
	}
	var errs []error
	account := override
	if override.Provider != p {
		var bound bool
		var err error
		account, bound, err = s.SessionAccount(ctx, id, p)
		if err == nil && bound && s.beingRemoved(account.ID) {
			// Its account is going away: start on the default, and say so.
			var def string
			if def, err = s.defaultID(ctx, p); err == nil {
				account, err = s.find(ctx, def)
				bound = false
			}
		}
		if err != nil {
			return nil, err
		}
		if !bound {
			if err := s.store.BindSessionAgentAccount(ctx, id, p, account.ID, s.now()); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if account.Device() {
		// The computer's own login: no folder to prepare, but the provider may
		// blank variables inherited from the daemon (another account's home).
		return provider.LaunchEnv(""), errors.Join(errs...)
	}
	// ponytail: a later link failure is only logged here, as this path cannot
	// refuse the launch; binding and moves already refused it.
	if err := s.prepare(provider, account.Home); err != nil {
		errs = append(errs, err)
		s.warnSplitOnce(account, err)
	}
	return provider.LaunchEnv(account.Home), errors.Join(errs...)
}
