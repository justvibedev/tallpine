package codex

import (
	"context"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

func (s *Service) prepareCodexAccountLogin(ctx context.Context) error {
	if s.codexAccounts == nil {
		return apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account management is unavailable")
	}
	if s.codexSwitches != nil && s.codexSwitches.CodexAccountSwitchInProgress() {
		return apierr.Conflict("CODEX_ACCOUNT_SWITCH_IN_PROGRESS", "A Codex account switch is already in progress", nil)
	}
	if err := s.WaitCodexAccountStoreReady(ctx); err != nil {
		return err
	}
	if err := s.requireCodexAccountInstallation(ctx); err != nil {
		return err
	}
	capabilities := s.codexAccounts.detectCapabilities(ctx)
	switch capabilities.NativeLogin.State {
	case domain.CodexCapabilityUnsupported:
		return apierr.NotImplemented("CODEX_ACCOUNT_MANAGEMENT_UNSUPPORTED", "This Codex version does not support account management")
	case domain.CodexCapabilityUnknown:
		return apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account management capability could not be verified")
	default:
		return nil
	}
}

// OpenCodexAccountLoginTerminal starts one private native-login operation.
func (s *Service) OpenCodexAccountLoginTerminal(ctx context.Context) (AccountLoginTerminalStart, error) {
	if err := s.prepareCodexAccountLogin(ctx); err != nil {
		return AccountLoginTerminalStart{}, err
	}
	return s.codexAccounts.openLoginTerminal(ctx, "")
}

// OpenCodexAccountReauthenticationTerminal starts native sign-in for one
// retained account slot. A locally validated credential replaces that slot
// instead of creating a duplicate account.
func (s *Service) OpenCodexAccountReauthenticationTerminal(ctx context.Context, accountID string) (AccountLoginTerminalStart, error) {
	if err := s.prepareCodexAccountLogin(ctx); err != nil {
		return AccountLoginTerminalStart{}, err
	}
	accountID = strings.TrimSpace(accountID)
	if err := s.EnsureCodexDeviceAccountReconciled(ctx); err != nil {
		return AccountLoginTerminalStart{}, err
	}
	return s.codexAccounts.openLoginTerminal(ctx, accountID)
}

// LogoutCodexAccount removes one Tallpine-saved credential while retaining the
// account card. Active-account logout also clears the device-global file-backed
// credential after exact structured identity confirmation.
func (s *Service) LogoutCodexAccount(ctx context.Context, accountID string) (Accounts, error) {
	if s.codexAccounts == nil || s.codexAccounts.factory == nil {
		return Accounts{}, apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account management is unavailable")
	}
	if s.codexSwitches != nil && s.codexSwitches.CodexAccountSwitchInProgress() {
		return Accounts{}, apierr.Conflict("CODEX_ACCOUNT_SWITCH_IN_PROGRESS", "A Codex account switch is already in progress", nil)
	}
	if s.CodexAccountLoginInProgress() {
		return Accounts{}, apierr.Conflict("CODEX_ACCOUNT_LOGIN_IN_PROGRESS", "Finish or close the Codex account login before logging out", nil)
	}
	if err := s.WaitCodexAccountStoreReady(ctx); err != nil {
		return Accounts{}, err
	}
	accountID = strings.TrimSpace(accountID)
	// Only the verified or last-known device owner needs global reconciliation.
	// An inactive account logs out through its isolated Tallpine home even when the
	// device-global credential cannot currently be inspected.
	if s.codexAccounts.accountMayOwnDeviceCredential(accountID) {
		if err := s.EnsureCodexDeviceAccountReconciled(ctx); err != nil {
			return Accounts{}, err
		}
	}
	if err := s.codexAccounts.logout(ctx, accountID); err != nil {
		return Accounts{}, err
	}
	s.invalidateAuthentication()
	return s.CachedCodexAccounts(ctx)
}

// DeleteCodexAccount permanently removes one inactive signed-out account slot.
func (s *Service) DeleteCodexAccount(ctx context.Context, accountID string) (Accounts, error) {
	if s.codexAccounts == nil {
		return Accounts{}, apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account management is unavailable")
	}
	if s.codexSwitches != nil && s.codexSwitches.CodexAccountSwitchInProgress() {
		return Accounts{}, apierr.Conflict("CODEX_ACCOUNT_SWITCH_IN_PROGRESS", "A Codex account switch is already in progress", nil)
	}
	if s.CodexAccountLoginInProgress() {
		return Accounts{}, apierr.Conflict("CODEX_ACCOUNT_LOGIN_IN_PROGRESS", "Finish or close the Codex account login before deleting an account", nil)
	}
	if err := s.WaitCodexAccountStoreReady(ctx); err != nil {
		return Accounts{}, err
	}
	accountID = strings.TrimSpace(accountID)
	if s.codexAccounts.accountMayOwnDeviceCredential(accountID) {
		if err := s.EnsureCodexDeviceAccountReconciled(ctx); err != nil {
			return Accounts{}, err
		}
	}
	if err := s.codexAccounts.deleteAccount(ctx, accountID); err != nil {
		return Accounts{}, err
	}
	return s.CachedCodexAccounts(ctx)
}

// VerifyCodexAccountLogin completes a pending login once its local credential
// has been safely validated and committed.
func (s *Service) VerifyCodexAccountLogin(ctx context.Context, operationID string) (domain.CodexAccountLoginOperation, error) {
	if s.codexAccounts == nil {
		return domain.CodexAccountLoginOperation{}, apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account management is unavailable")
	}
	result, err := s.codexAccounts.verifyLogin(ctx, strings.TrimSpace(operationID))
	if err == nil && result.Status == domain.CodexAccountLoginCompleted && result.Account != nil {
		if result.Account.Active && s.invalidateAuthentication != nil {
			s.invalidateAuthentication()
		}
		// Credential commit is complete. Provider-backed authentication, capacity,
		// and usage warming happens independently and cannot roll the login back.
		accountID := result.Account.ID
		go func() {
			_, _ = s.EnsureCodexAccounts(s.codexAccounts.ctx, []string{accountID}, AccountEnsureOptions{
				IncludeUsage: true, ForceAuthentication: true, ForceDeviceReconciliation: true,
			})
		}()
	}
	return result, err
}

// CancelCodexAccountLogin destroys a pending login and its credential staging.
func (s *Service) CancelCodexAccountLogin(ctx context.Context, operationID string) (domain.CodexAccountLoginOperation, error) {
	if s.codexAccounts == nil {
		return domain.CodexAccountLoginOperation{}, apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account management is unavailable")
	}
	return s.codexAccounts.cancelLogin(ctx, strings.TrimSpace(operationID))
}
func (s *Service) requireCodexAccountInstallation(ctx context.Context) error {
	observations, err := s.installation(ctx)
	if err != nil {
		return err
	}
	if observations.State == domain.AgentInstallationNotInstalled && observations.Freshness == domain.AgentReadinessFresh {
		return apierr.NotImplemented("CODEX_ACCOUNT_MANAGEMENT_UNSUPPORTED", "Codex is not installed")
	}
	return nil
}
