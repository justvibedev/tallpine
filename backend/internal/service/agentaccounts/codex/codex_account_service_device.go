package codex

import (
	"context"
	"errors"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// InvalidateCodexAccountAuthentication invalidates the globally active account.
func (s *Service) InvalidateCodexAccountAuthentication() {
	if s.codexAccounts == nil {
		return
	}
	id := s.codexAccounts.activeAccountID()
	if id != "" {
		s.codexAccounts.invalidate(id)
	}
	s.invalidateAuthentication()
	go func() { _ = s.codexAccounts.reconcileGlobal(s.codexAccounts.ctx) }()
}

// ObserveActiveCodexAccountCapacity attributes a provider event to the active account.
func (s *Service) ObserveActiveCodexAccountCapacity(observation ports.CodexCapacityObservation) {
	if s.codexAccounts == nil {
		return
	}
	id := s.codexAccounts.activeAccountID()
	if id != "" {
		s.codexAccounts.capacity.updateFromEvent(id, observation)
	}
}

// WarmCodexAccounts starts asynchronous local-store initialization, local
// device reconciliation, and then saved-account observation warming.
func (s *Service) WarmCodexAccounts() {
	if s.codexAccounts == nil {
		return
	}
	go func() {
		if err := s.codexAccounts.waitAccountStore(s.codexAccounts.ctx); err != nil {
			return
		}
		// Reconciliation is local-only and establishes the safe home for the device
		// account before any Codex process is opened for authentication or capacity.
		// A local reconciliation failure still leaves inactive saved accounts
		// eligible for their isolated checks below.
		_ = s.codexAccounts.reconcileGlobal(s.codexAccounts.ctx)
		capabilities := s.codexAccounts.detectCapabilities(s.codexAccounts.ctx)
		records, err := s.codexAccounts.catalog.recordsFor(nil)
		if err != nil {
			return
		}
		if capabilities.AccountRead.State == domain.CodexCapabilitySupported {
			for _, record := range records {
				if record.Snapshot.Status == domain.CodexAccountStatusValid {
					_, _ = s.codexAccounts.ensureAuthentication(s.codexAccounts.ctx, record, domain.AgentReadinessPurposeDisplay)
				}
			}
		}
		_ = s.codexAccounts.capacity.ensure(s.codexAccounts.ctx, records, capabilities, false)
	}()
}

// WaitCodexAccountStoreReady waits only for Tallpine-owned local account state. It
// never starts Codex or inspects the device-global credential.
func (s *Service) WaitCodexAccountStoreReady(ctx context.Context) error {
	if s.codexAccounts == nil {
		return apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account management is unavailable")
	}
	err := s.codexAccounts.waitAccountStore(ctx)
	if err == nil {
		return nil
	}
	var failure *codexAccountLocalFailure
	if !errors.As(err, &failure) {
		return err
	}
	return apierr.New(apierr.KindUnavailable, "CODEX_ACCOUNT_SETUP_INCOMPLETE", "Codex account setup did not complete", map[string]any{
		"reasonCode": failure.reason, "retryable": failure.retryable,
	})
}

// ReconcileCodexDeviceCredentialForRecheck matches the device Codex credential
// before an explicit authentication recheck, e.g. after a login in the Harness
// settings terminal, which runs `codex login` outside Tallpine's account login flow.
// Codex readiness answers from the last device reconciliation, and nothing else
// re-runs it after such a login, so without this the recheck kept reporting the
// pre-login state (no device credential, or a previous credential's "sign in
// again" evidence) until something else reconciled or the app restarted. A
// changed credential is matched to its account; an unchanged one keeps its
// evidence. Best-effort: account management is optional for ordinary Codex use,
// so failures leave the recheck unchanged.
// ReconcileCodexDeviceCredentialForRecheck refreshes the device credential before a recheck.
func (s *Service) ReconcileCodexDeviceCredentialForRecheck(ctx context.Context) {
	if s.codexAccounts == nil {
		return
	}
	if err := s.WaitCodexAccountStoreReady(ctx); err != nil {
		return
	}
	_ = s.codexAccounts.reconcileGlobalWithPolicy(ctx, true)
}

// EnsureCodexDeviceAccountReconciled conclusively identifies the canonical
// device account before an operation is allowed to mutate it.
func (s *Service) EnsureCodexDeviceAccountReconciled(ctx context.Context) error {
	if err := s.WaitCodexAccountStoreReady(ctx); err != nil {
		return err
	}
	err := s.codexAccounts.reconcileGlobal(ctx)
	s.codexAccounts.mu.Lock()
	state := s.codexAccounts.reconciliation
	s.codexAccounts.mu.Unlock()
	if err == nil && state.Status == domain.CodexDeviceReconciliationVerified {
		return nil
	}
	reason, retryable := state.ReasonCode, state.Retryable
	if err != nil {
		failure := classifyDeviceReconciliationFailure(err)
		reason, retryable = failure.reason, failure.retryable
	}
	if strings.TrimSpace(reason) == "" {
		reason = "account_reconciliation_unavailable"
	}
	return apierr.New(apierr.KindUnavailable, "CODEX_DEVICE_ACCOUNT_UNVERIFIED", "The device Codex account could not be verified", map[string]any{
		"reasonCode": reason, "retryable": retryable,
	})
}

// BeginCodexAccountMutation gives the local switch worker exclusive ownership
// of the credential mutation path for the complete transaction.
func (s *Service) BeginCodexAccountMutation(ctx context.Context) error {
	if s.codexAccounts == nil {
		return apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account management is unavailable")
	}
	_, err := s.codexAccounts.acquireAccountMutation(ctx)
	return err
}

// EndCodexAccountMutation releases the switch-owned credential mutation path.
func (s *Service) EndCodexAccountMutation() {
	if s.codexAccounts == nil {
		return
	}
	select {
	case s.codexAccounts.mutations <- struct{}{}:
	default:
	}
}

// CodexAccountLoginInProgress reports whether a native login is still open.
func (s *Service) CodexAccountLoginInProgress() bool {
	if s.codexAccounts == nil {
		return false
	}
	s.codexAccounts.mu.Lock()
	defer s.codexAccounts.mu.Unlock()
	return s.codexAccounts.login != nil && !terminalLoginStatus(s.codexAccounts.login.snapshot.Status)
}
