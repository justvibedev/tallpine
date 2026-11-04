package codex

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (m *codexAccountManager) finishAuthentication(ctx context.Context, id string, observation domain.AgentAuthenticationObservation, method domain.CodexAuthMethod, email *string, failed bool, call *accountAuthCall) {
	m.mu.Lock()
	state := m.auth[id]
	if !state.reauthenticationRequired {
		if failed {
			m.catalog.updateSnapshot(id, func(s *domain.CodexAccountSnapshot) { preserveAuthenticationFailure(&s.Authentication, observation) })
			state.invalidated = true
			state.failures++
			if state.failures <= len(DefaultReadinessRetryDelays) {
				state.nextRetryAt = m.now().Add(DefaultReadinessRetryDelays[state.failures-1])
			}
		} else {
			// An unauthorized read reports no account, so it carries no identity. The
			// saved slot still belongs to the same account, and discarding its label
			// would both hide who must sign in again and break global reconciliation's
			// identity match.
			identified := observation.State == domain.AgentAuthenticationAuthorized || observation.State == domain.AgentAuthenticationNotApplicable
			m.catalog.updateSnapshot(id, func(s *domain.CodexAccountSnapshot) {
				s.Authentication = observation
				if identified {
					s.AuthMethod = method
					s.AccountEmail = email
					s.Label = accountLabel(email)
				}
			})
			if identified {
				// Reconciliation may create the account from local auth.json before
				// Codex supplies display metadata. Persist the first successful
				// account/read result so a later catalog refresh or daemon restart
				// cannot regress the label to the internal account-id fallback.
				if err := m.catalog.updateVerifiedDescriptor(ctx, id, ports.CodexAccountObservation{Method: method, Email: email}); err != nil {
					m.logger.Warn("Codex account display metadata could not be persisted", "accountID", id)
				}
			}
			state.invalidated = false
			state.launchVerified = false
			state.failures = 0
			state.nextRetryAt = time.Time{}
		}
	}
	state.call = nil
	close(call.done)
	m.mu.Unlock()
	m.publish()
}

type codexAuthenticationEvidence uint8

const codexAuthenticationCredentialRejected codexAuthenticationEvidence = 1

// recordProtectedAuthenticationEvidence is the single owner for authentication
// conclusions learned by other protected Codex calls. Capacity only reports the
// typed rejection; it never refreshes credentials or mutates authentication.
func (m *codexAccountManager) recordProtectedAuthenticationEvidence(id string, evidence codexAuthenticationEvidence) {
	if evidence == codexAuthenticationCredentialRejected {
		m.requireReauthentication(id)
	}
}

func (m *codexAccountManager) authenticationVerification(id string) (verified, reauthenticationRequired bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.auth[id]
	if state == nil {
		return false, false
	}
	return state.launchVerified, state.reauthenticationRequired
}

func (m *codexAccountManager) authenticationChanged() {
	if m.onAuthenticationChanged != nil {
		m.onAuthenticationChanged()
	}
	m.publish()
}

func (m *codexAccountManager) invalidate(id string) {
	m.mu.Lock()
	state := m.auth[id]
	if state == nil {
		state = &accountAuthState{}
		m.auth[id] = state
	}
	state.invalidated = true
	state.nextRetryAt = time.Time{}
	m.mu.Unlock()
	m.catalog.updateSnapshot(id, func(s *domain.CodexAccountSnapshot) { s.Authentication.Freshness = domain.AgentReadinessStale })
	m.capacity.invalidate(id, true)
	m.publish()
}

// invalidateCredentialEvidence clears conclusions made about credentials that
// reconciliation has just replaced. In particular, an expired-token result for
// the previous auth.json must not suppress verification of a newly rotated or
// externally refreshed credential for the same account.
func (m *codexAccountManager) invalidateCredentialEvidence(id string) {
	m.mu.Lock()
	state := m.auth[id]
	if state == nil {
		state = &accountAuthState{}
		m.auth[id] = state
	}
	state.reauthenticationRequired = false
	state.launchVerified = false
	state.invalidated = true
	state.failures = 0
	state.nextRetryAt = time.Time{}
	delete(m.usage, id)
	m.mu.Unlock()
	m.catalog.updateSnapshot(id, func(snapshot *domain.CodexAccountSnapshot) {
		snapshot.Authentication = uncheckedAuthentication()
		snapshot.UsageSummary = nil
	})
	m.capacity.invalidate(id, true)
	m.publish()
}

func (m *codexAccountManager) requireReauthentication(id string) {
	m.mu.Lock()
	state := m.auth[id]
	if state == nil {
		state = &accountAuthState{}
		m.auth[id] = state
	}
	state.reauthenticationRequired = true
	state.invalidated = false
	state.launchVerified = true
	state.failures = 0
	state.nextRetryAt = time.Time{}
	delete(m.usage, id)
	m.mu.Unlock()
	m.catalog.updateSnapshot(id, func(snapshot *domain.CodexAccountSnapshot) {
		snapshot.Authentication = signedOutAuthentication(m.now(), "Codex could not refresh this account. Sign in again to continue.")
		snapshot.Capacity = unavailableCodexCapacity()
		snapshot.UsageSummary = nil
	})
	m.capacity.replace(id, staticCodexCapacity(domain.CodexCapacityUnknown, domain.CodexCapacityReasonSkippedSignedOut, "Sign in to Codex to see subscription capacity."), "reauthentication_required")
	m.authenticationChanged()
}

func (m *codexAccountManager) clearReauthenticationRequired(id string) {
	m.mu.Lock()
	state := m.auth[id]
	if state == nil {
		state = &accountAuthState{}
		m.auth[id] = state
	}
	state.reauthenticationRequired = false
	state.invalidated = false
	state.launchVerified = false
	state.failures = 0
	state.nextRetryAt = time.Time{}
	m.mu.Unlock()
}
