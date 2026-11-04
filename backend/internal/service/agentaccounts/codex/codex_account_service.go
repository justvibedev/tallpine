package codex

import (
	"context"
	"log/slog"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Service is the Codex account facade the agent service embeds: login,
// logout, switch, capacity and the account-aware readiness check.
type Service struct {
	codexAccounts *codexAccountManager
	codexSwitches *codexAccountSwitchCoordinator
	// installation reports the current Codex installation observation.
	installation func(ctx context.Context) (domain.AgentInstallationObservation, error)
	// invalidateAuthentication drops the cached Codex authentication verdict.
	invalidateAuthentication func()
	// savedDefaultSignIn reports the sign-in of the saved account new
	// sessions start on.
	savedDefaultSignIn func(ctx context.Context, harness domain.AgentHarness) (domain.AgentAccountIdentityCheck, bool)
}

// Deps wires a Service to the agent readiness coordinator and the daemon.
type Deps struct {
	Context           context.Context
	Logger            *slog.Logger
	Clock             func() time.Time
	AccountRoot       string
	PendingRoot       string
	SwitchStagingRoot string
	GlobalHome        string
	Factory           ports.CodexAccountClientFactory
	OperationGate     ports.CodexOperationGate
	Switches          ports.CodexAccountSwitchStore
	// AccountStore persists managed accounts as AgentAccount rows. Without it
	// they live in memory only.
	AccountStore             ports.CodexAccountRegistryStore
	Installation             func(ctx context.Context) (domain.AgentInstallationObservation, error)
	InvalidateAuthentication func()
}

// NewService builds the Codex account service. With no account or global home
// root it still returns a Service whose account methods report unavailable.
func NewService(deps Deps) *Service {
	s := &Service{installation: deps.Installation, invalidateAuthentication: deps.InvalidateAuthentication}
	if s.invalidateAuthentication == nil {
		s.invalidateAuthentication = func() {}
	}
	if deps.AccountRoot == "" || deps.GlobalHome == "" {
		return s
	}
	s.codexAccounts = newCodexAccountManager(deps.Context, deps.AccountRoot, deps.PendingRoot, deps.SwitchStagingRoot, deps.GlobalHome, deps.Factory, deps.Logger, deps.OperationGate)
	if deps.Clock != nil {
		s.codexAccounts.now = deps.Clock
	}
	if deps.AccountStore != nil {
		s.codexAccounts.catalog.descriptors = accountRowDescriptors{store: deps.AccountStore}
	}
	s.codexAccounts.onAuthenticationChanged = s.invalidateAuthentication
	if deps.Switches != nil && deps.OperationGate != nil {
		s.codexSwitches = newCodexAccountSwitchCoordinator(deps.Context, s, deps.Switches, deps.OperationGate, deps.Clock, s.PublishCodexAccounts)
	}
	return s
}

// InvalidateActiveAccount drops the cached verdict of the active account.
func (s *Service) InvalidateActiveAccount() {
	if s.codexAccounts == nil {
		return
	}
	if id := s.codexAccounts.activeAccountID(); id != "" {
		s.codexAccounts.invalidate(id)
	}
}

// AuthenticationWithAccount decides readiness by the saved account new
// sessions start on, when they start on one, and otherwise by Codex's
// structured account check.
func (s *Service) AuthenticationWithAccount(ctx context.Context, agentID string, purpose domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, bool) {
	if observation, handled := s.savedDefaultAuthentication(ctx, agentID); handled {
		return observation, true
	}
	return s.codexAuthenticationWithAccount(ctx, agentID, purpose)
}

// codexAuthenticationWithAccount adds the identity of the account a
// structured Codex verdict checked, when it found that account signed in.
func (s *Service) codexAuthenticationWithAccount(ctx context.Context, agentID string, purpose domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, bool) {
	observation, record, handled := s.structuredCodexAuthenticationRecord(ctx, agentID, purpose)
	if handled && record.Snapshot.ID != "" && (observation.State == domain.AgentAuthenticationAuthorized || observation.State == domain.AgentAuthenticationConfigured) {
		observation.Account = codexAccountIdentity(record)
		observation.Diagnostics = append(observation.Diagnostics, domain.NewAgentAuthDiagnostic("codex_account", "account_context"))
	}
	return observation, handled
}

// SetSavedDefaultSignIn late-binds the per-folder account catalog. When new
// Claude Code or Codex sessions start on a saved account, that account's
// sign-in, not the computer's own login, is whether the harness is ready.
func (s *Service) SetSavedDefaultSignIn(signedIn func(ctx context.Context, harness domain.AgentHarness) (domain.AgentAccountIdentityCheck, bool)) {
	s.savedDefaultSignIn = signedIn
}

func (s *Service) savedDefaultAuthentication(ctx context.Context, agentID string) (domain.AgentAuthenticationObservation, bool) {
	harness := domain.AgentHarness(agentID)
	if _, ok := domain.AgentAccountProviderFor(harness); !ok || s.savedDefaultSignIn == nil {
		return domain.AgentAuthenticationObservation{}, false
	}
	identity, handled := s.savedDefaultSignIn(ctx, harness)
	if !handled {
		return domain.AgentAuthenticationObservation{}, false
	}
	now := time.Now().UTC()
	var observation domain.AgentAuthenticationObservation
	if identity.SignedIn {
		observation = domain.SuccessfulAuthentication(now, domain.AgentAuthenticationAuthorized, domain.AgentReadinessReasonAuthorized, "New sessions start on a saved account that is signed in.")
		observation.Account = domain.AgentAccountIdentity{Email: identity.Email, Plan: identity.Plan, Method: domain.AgentAccountMethodAccount}.Sanitized()
	} else {
		observation = domain.SuccessfulAuthentication(now, domain.AgentAuthenticationUnauthorized, domain.AgentReadinessReasonUnauthorized, "The default account is signed out, and no other account is signed in. Sign one in again in Settings.")
	}
	// Marks the verdict as the saved account's: a sign-in or sign-out of the
	// CLI's own login does not change it, so clients check that login itself.
	observation.Diagnostics = []domain.AgentAuthDiagnostic{domain.NewAgentAuthDiagnostic("agent_account", "saved_default_account")}
	return observation, true
}

func codexAccountIdentity(record codexAccountRecord) *domain.AgentAccountIdentity {
	var identity domain.AgentAccountIdentity
	if record.Snapshot.AccountEmail != nil {
		identity.Email = *record.Snapshot.AccountEmail
	}
	identity.Method = domain.CodexAccountMethod(record.Snapshot.AuthMethod)
	if record.Snapshot.Capacity.Plan != nil {
		identity.Plan = *record.Snapshot.Capacity.Plan
	}
	return identity.Sanitized()
}

// Service integration.
// structuredCodexAuthenticationRecord also returns the account record the
// verdict is about; it is zero when no account was checked.
func (s *Service) structuredCodexAuthenticationRecord(ctx context.Context, agentID string, purpose domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, codexAccountRecord, bool) {
	none := codexAccountRecord{}
	if agentID != string(domain.HarnessCodex) || s.codexAccounts == nil || s.codexAccounts.factory == nil {
		return domain.AgentAuthenticationObservation{}, none, false
	}
	if err := s.WaitCodexAccountStoreReady(ctx); err != nil {
		ports.RecordAuthDiagnostic(ctx, domain.NewAgentAuthDiagnostic("codex_account", "account_store_unavailable"))
		// Account management is optional for ordinary Codex use. When Tallpine's
		// local account store cannot answer safely, fall back to the native
		// readiness path instead of treating that as proof Codex is signed out.
		return domain.AgentAuthenticationObservation{}, none, false
	}
	if purpose == domain.AgentReadinessPurposeLaunch {
		s.codexAccounts.mu.Lock()
		verified := s.codexAccounts.reconciliation.Status == domain.CodexDeviceReconciliationVerified
		s.codexAccounts.mu.Unlock()
		if !verified {
			return domain.AgentAuthenticationObservation{}, none, false
		}
	}
	id := s.codexAccounts.activeAccountID()
	if id == "" {
		s.codexAccounts.mu.Lock()
		reconciled := s.codexAccounts.reconciliation.Status == domain.CodexDeviceReconciliationVerified
		credentialPresent := s.codexAccounts.deviceCredentialPresent
		s.codexAccounts.mu.Unlock()
		if !reconciled || credentialPresent {
			observation := uncheckedAuthentication()
			observation.Diagnostics = []domain.AgentAuthDiagnostic{domain.NewAgentAuthDiagnostic("codex_account", "account_sync_pending")}
			return observation, none, true
		}
		return domain.SuccessfulAuthentication(s.codexAccounts.now(), domain.AgentAuthenticationUnauthorized, domain.AgentReadinessReasonUnauthorized, "Sign in to Codex or add an account in Settings."), none, true
	}
	record, ok := s.codexAccounts.catalog.record(id)
	if !ok {
		return domain.FailedAuthentication(s.codexAccounts.now(), domain.AgentReadinessReasonAuthCheckInconclusive, "The active Codex account is unavailable."), none, true
	}
	result, err := s.codexAccounts.ensureAuthentication(ctx, record, purpose)
	if err != nil {
		return domain.FailedAuthentication(s.codexAccounts.now(), domain.AgentReadinessReasonAuthCheckFailed, "Authentication check failed."), none, true
	}
	record, ok = s.codexAccounts.catalog.record(id)
	if !ok {
		return domain.AgentAuthenticationObservation{}, none, false
	}
	if purpose == domain.AgentReadinessPurposeLaunch && result.State == domain.AgentAuthenticationAuthorized && record.Snapshot.AuthMethod == domain.CodexAuthMethodChatGPT {
		capabilities := s.codexAccounts.detectCapabilities(ctx)
		if capabilities.CapacityRead.State != domain.CodexCapabilitySupported {
			return domain.AgentAuthenticationObservation{}, none, false
		}
		// Launch readiness uses a protected provider call. account/read is only
		// local metadata discovery and cannot prove that the server accepts the
		// stored tokens.
		s.codexAccounts.capacity.invalidate(record.Snapshot.ID, false)
		_, capacityErr := s.codexAccounts.capacity.ensureOne(ctx, record, capabilities, true)
		if capacityErr != nil {
			return domain.AgentAuthenticationObservation{}, none, false
		}
		latest, ok := s.codexAccounts.catalog.record(record.Snapshot.ID)
		if !ok {
			return domain.AgentAuthenticationObservation{}, none, false
		}
		verified, reauthenticationRequired := s.codexAccounts.authenticationVerification(record.Snapshot.ID)
		if reauthenticationRequired {
			return latest.Snapshot.Authentication, latest, true
		}
		if !verified {
			// Offline, timeout, and provider failures are not evidence that the
			// account is signed out. Let native launch readiness remain advisory.
			return domain.AgentAuthenticationObservation{}, none, false
		}
		return latest.Snapshot.Authentication, latest, true
	}
	return result, record, true
}
