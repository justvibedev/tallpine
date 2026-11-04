package codex

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// CachedCodexAccounts returns the current in-memory view without native work.
func (s *Service) CachedCodexAccounts(ctx context.Context) (Accounts, error) {
	if err := ctx.Err(); err != nil {
		return Accounts{}, err
	}
	if s.codexAccounts == nil {
		return Accounts{}, apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account management is unavailable")
	}
	if err := s.WaitCodexAccountStoreReady(ctx); err != nil {
		return Accounts{}, err
	}
	result := s.codexAccounts.cached()
	if s.codexSwitches != nil {
		if sw, ok, err := s.codexSwitches.GetActiveCodexAccountSwitch(ctx); err == nil && ok {
			result.CurrentSwitch = &sw
		}
	}
	return result, nil
}

// AccountEnsureOptions selects the account observations refreshed by EnsureCodexAccounts.
type AccountEnsureOptions struct {
	IncludeUsage              bool
	ForceAuthentication       bool
	ForceDeviceReconciliation bool
}

// EnsureCodexAccounts rediscovers requested accounts and refreshes eligible observations.
func (s *Service) EnsureCodexAccounts(ctx context.Context, ids []string, options AccountEnsureOptions) (Accounts, error) {
	if s.codexAccounts == nil {
		return Accounts{}, apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account management is unavailable")
	}
	if s.codexSwitches != nil && s.codexSwitches.CodexAccountSwitchInProgress() {
		if sw, ok, err := s.codexSwitches.GetActiveCodexAccountSwitch(ctx); err == nil && ok {
			result := s.codexAccounts.cached()
			result.CurrentSwitch = &sw
			return result, nil
		}
		return s.codexAccounts.cached(), nil
	}
	if err := s.WaitCodexAccountStoreReady(ctx); err != nil {
		return Accounts{}, err
	}
	// Full Settings refreshes establish the current device account before any
	// global-home checks. A targeted row refresh stays in that saved account's
	// isolated home, so it must not publish a transient device reconciliation.
	if len(ids) == 0 || options.ForceDeviceReconciliation {
		_ = s.codexAccounts.reconcileGlobalWithPolicy(ctx, options.ForceDeviceReconciliation)
	}
	installation, err := s.installation(ctx)
	if err != nil {
		return Accounts{}, err
	}
	result, err := s.codexAccounts.ensure(ctx, ids, options.IncludeUsage, options.ForceAuthentication, installation.State)
	if err == nil && s.codexSwitches != nil {
		if sw, ok, switchErr := s.codexSwitches.GetActiveCodexAccountSwitch(ctx); switchErr == nil && ok {
			result.CurrentSwitch = &sw
		}
	}
	return result, err
}

// ConsumeCodexAccountResetCredit redeems one provider-reported usage-limit
// reset and returns the refreshed cached account view. The provider chooses the
// credit; opaque credit identifiers never cross the daemon boundary.
func (s *Service) ConsumeCodexAccountResetCredit(ctx context.Context, accountID, idempotencyKey string) (Accounts, error) {
	if s.codexAccounts == nil {
		return Accounts{}, apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account management is unavailable")
	}
	if s.codexSwitches != nil && s.codexSwitches.CodexAccountSwitchInProgress() {
		return Accounts{}, apierr.Conflict("CODEX_ACCOUNT_SWITCH_IN_PROGRESS", "Wait for the Codex account switch to finish before using a reset", nil)
	}
	if err := s.WaitCodexAccountStoreReady(ctx); err != nil {
		return Accounts{}, err
	}
	if err := s.codexAccounts.consumeResetCredit(ctx, accountID, idempotencyKey); err != nil {
		return Accounts{}, err
	}
	return s.CachedCodexAccounts(ctx)
}

// SubscribeCodexAccounts returns cached state followed by latest-wins updates.
func (s *Service) SubscribeCodexAccounts(ctx context.Context) (<-chan Accounts, error) {
	if s.codexAccounts == nil {
		return nil, apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account management is unavailable")
	}
	if err := s.WaitCodexAccountStoreReady(ctx); err != nil {
		return nil, err
	}
	source := s.codexAccounts.subscribe(ctx)
	out := make(chan Accounts, 1)
	go func() {
		defer close(out)
		for snapshot := range source {
			if s.codexSwitches != nil {
				if sw, ok, err := s.codexSwitches.GetActiveCodexAccountSwitch(ctx); err == nil && ok {
					snapshot.CurrentSwitch = &sw
				}
			}
			select {
			case out <- snapshot:
			default:
				select {
				case <-out:
				default:
				}
				select {
				case out <- snapshot:
				default:
				}
			}
		}
	}()
	return out, nil
}

// PublishCodexAccounts notifies subscribers after externally owned switch changes.
func (s *Service) PublishCodexAccounts() {
	if s.codexAccounts != nil {
		s.codexAccounts.publish()
	}
}

// SetCodexAccountLoginTerminalOpener wires the trusted shell-terminal boundary.
func (s *Service) SetCodexAccountLoginTerminalOpener(opener codexAccountLoginTerminalService) {
	if s.codexAccounts != nil {
		s.codexAccounts.terminal = opener
	}
}
