package codex

import (
	"context"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (m *codexAccountManager) ensure(ctx context.Context, ids []string, includeUsage, forceAuthentication bool, installation domain.AgentInstallationState) (Accounts, error) {
	if err := m.catalog.refresh(); err != nil {
		return Accounts{}, apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account discovery is unavailable")
	}
	records, err := m.catalog.recordsFor(ids)
	if err != nil {
		return Accounts{}, mapUnknownCodexAccount(err)
	}
	eligible := make([]codexAccountRecord, 0, len(records))
	for _, record := range records {
		// A targeted metadata refresh belongs to the saved account, not the
		// device-global credential. Keeping it isolated avoids turning a row
		// expansion into a device-account state transition.
		record.useSavedHome = len(ids) > 0
		if !m.deferAccountRead(record.Snapshot.ID) {
			eligible = append(eligible, record)
		}
	}
	if installation == domain.AgentInstallationNotInstalled {
		m.recordAuthenticationUnavailable(eligible, domain.AgentReadinessReasonAuthSkippedNotInstalled, "Authentication was not checked because Codex is not installed.")
		return m.view(ids)
	}
	capabilities := m.detectCapabilities(ctx)
	if capabilities.AccountRead.State != domain.CodexCapabilitySupported {
		code, reason := domain.AgentReadinessReasonAuthCheckInconclusive, "Authentication could not be checked."
		if capabilities.AccountRead.State == domain.CodexCapabilityUnsupported {
			code, reason = domain.AgentReadinessReasonAuthCheckUnsupported, "This Codex version cannot check authentication."
		}
		m.recordAuthenticationUnavailable(eligible, code, reason)
		return m.view(ids)
	}
	if forceAuthentication {
		m.forceAuthenticationRetry(eligible)
	}
	for _, record := range eligible {
		if record.Snapshot.Status == domain.CodexAccountStatusValid {
			if _, err := m.ensureAuthentication(ctx, record, domain.AgentReadinessPurposeDisplay); err != nil {
				return Accounts{}, err
			}
		}
	}
	records, err = m.catalog.recordsFor(ids)
	if err != nil {
		return Accounts{}, mapUnknownCodexAccount(err)
	}
	// Authentication checks above update the catalog snapshots. Capacity must
	// consume those refreshed observations, while still excluding the one stale
	// pointer whose device ownership has not been established.
	eligible = eligible[:0]
	for _, record := range records {
		record.useSavedHome = len(ids) > 0
		if !m.deferAccountRead(record.Snapshot.ID) {
			eligible = append(eligible, record)
		}
	}
	if err := m.capacity.ensure(ctx, eligible, capabilities, forceAuthentication); err != nil && !errors.Is(err, context.Canceled) {
		m.logger.Debug("Codex account capacity ensure degraded", "failure_category", "capacity_read")
	}
	if includeUsage && capabilities.UsageRead.State == domain.CodexCapabilitySupported {
		for _, record := range eligible {
			if domain.CodexSnapshotLoginCheck(record.Snapshot).SignedIn {
				_ = m.ensureUsage(ctx, record)
			}
		}
	}
	result, err := m.view(ids)
	m.publish()
	return result, err
}

func (m *codexAccountManager) recordAuthenticationUnavailable(records []codexAccountRecord, code, reason string) {
	attempted := m.now()
	observation := domain.FailedAuthentication(attempted, code, reason)
	for _, record := range records {
		if record.Snapshot.Status != domain.CodexAccountStatusValid {
			continue
		}
		m.catalog.updateSnapshot(record.Snapshot.ID, func(snapshot *domain.CodexAccountSnapshot) {
			domain.PreserveAuthenticationFailure(&snapshot.Authentication, observation)
		})
	}
	m.publish()
}

func (m *codexAccountManager) forceAuthenticationRetry(records []codexAccountRecord) {
	m.mu.Lock()
	for _, record := range records {
		if record.Snapshot.Status != domain.CodexAccountStatusValid {
			continue
		}
		state := m.auth[record.Snapshot.ID]
		if state == nil {
			state = &accountAuthState{}
			m.auth[record.Snapshot.ID] = state
		}
		state.invalidated = true
		state.nextRetryAt = time.Time{}
	}
	m.mu.Unlock()
	for _, record := range records {
		if record.Snapshot.Status == domain.CodexAccountStatusValid {
			m.capacity.invalidate(record.Snapshot.ID, false)
		}
	}
}

func (m *codexAccountManager) ensureAuthentication(ctx context.Context, record codexAccountRecord, purpose domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, error) {
	for {
		if err := ctx.Err(); err != nil {
			return domain.AgentAuthenticationObservation{}, err
		}
		m.mu.Lock()
		state := m.auth[record.Snapshot.ID]
		if state == nil {
			state = &accountAuthState{invalidated: true}
			m.auth[record.Snapshot.ID] = state
		}
		current, _ := m.catalog.record(record.Snapshot.ID)
		if state.reauthenticationRequired {
			out := current.Snapshot.Authentication
			m.mu.Unlock()
			return out, nil
		}
		ttl := codexAccountDisplayTTL
		if purpose == domain.AgentReadinessPurposeLaunch {
			ttl = codexAccountLaunchTTL
		}
		fresh := current.Snapshot.Authentication.CheckedAt != nil && m.now().Sub(*current.Snapshot.Authentication.CheckedAt) < ttl
		if !state.invalidated && fresh {
			out := current.Snapshot.Authentication
			m.mu.Unlock()
			return out, nil
		}
		if purpose == domain.AgentReadinessPurposeDisplay && !state.nextRetryAt.IsZero() && m.now().Before(state.nextRetryAt) {
			out := current.Snapshot.Authentication
			m.mu.Unlock()
			return out, nil
		}
		if state.call != nil {
			call := state.call
			m.mu.Unlock()
			select {
			case <-call.done:
				if call.retry {
					continue
				}
				latest, _ := m.catalog.record(record.Snapshot.ID)
				return latest.Snapshot.Authentication, nil
			case <-ctx.Done():
				return domain.AgentAuthenticationObservation{}, ctx.Err()
			}
		}
		call := &accountAuthCall{done: make(chan struct{}), previous: current.Snapshot.Authentication}
		state.call = call
		m.catalog.updateSnapshot(record.Snapshot.ID, func(s *domain.CodexAccountSnapshot) { s.Authentication.Freshness = domain.AgentReadinessChecking })
		m.mu.Unlock()
		go m.runAuthentication(record, call)
		select {
		case <-call.done:
			if call.retry {
				continue
			}
			latest, _ := m.catalog.record(record.Snapshot.ID)
			return latest.Snapshot.Authentication, nil
		case <-ctx.Done():
			return domain.AgentAuthenticationObservation{}, ctx.Err()
		}
	}
}

func (m *codexAccountManager) runAuthentication(record codexAccountRecord, call *accountAuthCall) {
	attempted := m.now()
	ctx, cancel := context.WithTimeout(m.ctx, codexAccountAuthTimeout)
	defer cancel()
	select {
	case m.processes <- struct{}{}:
		defer func() { <-m.processes }()
	case <-ctx.Done():
		m.finishAuthentication(ctx, record.Snapshot.ID, domain.FailedAuthentication(attempted, domain.AgentReadinessReasonAuthCheckFailed, "Authentication check stopped."), domain.CodexAuthMethodUnknown, nil, true, call)
		return
	}
	account := m.accountContext(record)
	releaseGlobal, err := m.acquireGlobalRead(ctx, account)
	if err != nil {
		m.finishAuthentication(ctx, record.Snapshot.ID, domain.FailedAuthentication(attempted, domain.AgentReadinessReasonAuthCheckFailed, "Authentication check stopped."), domain.CodexAuthMethodUnknown, nil, true, call)
		return
	}
	releasedGlobal := false
	releaseGlobalOnce := func() {
		if !releasedGlobal {
			releaseGlobal()
			releasedGlobal = true
		}
	}
	defer releaseGlobalOnce()
	if m.globalCredentialMissingFor(account) {
		releaseGlobalOnce()
		_ = m.reconcileGlobalWithPolicy(m.ctx, true)
		m.retryAuthenticationAfterDeviceChange(record.Snapshot.ID, call)
		return
	}
	client, err := m.factory.Open(ctx, account)
	if err != nil {
		if m.globalCredentialMissingFor(account) {
			releaseGlobalOnce()
			_ = m.reconcileGlobalWithPolicy(m.ctx, true)
			m.retryAuthenticationAfterDeviceChange(record.Snapshot.ID, call)
			return
		}
		m.finishAuthentication(ctx, record.Snapshot.ID, domain.FailedAuthentication(attempted, domain.AgentReadinessReasonAuthCheckFailed, "Authentication check failed."), domain.CodexAuthMethodUnknown, nil, true, call)
		return
	}
	clientClosed := false
	closeClientOnce := func() {
		if !clientClosed {
			_ = client.Close()
			clientClosed = true
		}
	}
	defer closeClientOnce()
	// account/read is metadata discovery only. Remote authentication is proved
	// separately by a protected account call.
	observation, err := client.Read(ctx, false)
	if m.globalCredentialMissingFor(account) {
		closeClientOnce()
		releaseGlobalOnce()
		_ = m.reconcileGlobalWithPolicy(m.ctx, true)
		m.retryAuthenticationAfterDeviceChange(record.Snapshot.ID, call)
		return
	}
	if err != nil {
		code, reason := domain.AgentReadinessReasonAuthCheckFailed, "Authentication check failed."
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code, reason = domain.AgentReadinessReasonAuthCheckTimeout, "Authentication check timed out."
		}
		m.finishAuthentication(ctx, record.Snapshot.ID, domain.FailedAuthentication(attempted, code, reason), domain.CodexAuthMethodUnknown, nil, true, call)
		return
	}
	result := accountAuthenticationObservation(m.now(), observation.Authentication)
	m.finishAuthentication(ctx, record.Snapshot.ID, result, observation.Method, observation.Email, observation.Authentication == domain.AgentAuthenticationUnknown, call)
}

func (m *codexAccountManager) globalCredentialMissingFor(account ports.CodexAccountContext) bool {
	if canonicalPath(account.Home) != m.globalHome {
		return false
	}
	_, state, err := readCodexDeviceFileState(m.globalCredentialPath(), true)
	return err == nil && !state.exists
}

func (m *codexAccountManager) retryAuthenticationAfterDeviceChange(id string, call *accountAuthCall) {
	m.mu.Lock()
	state := m.auth[id]
	if state != nil && state.call == call {
		m.catalog.updateSnapshot(id, func(snapshot *domain.CodexAccountSnapshot) {
			snapshot.Authentication = call.previous
		})
		state.invalidated = true
		state.nextRetryAt = time.Time{}
		state.call = nil
		call.retry = true
		close(call.done)
	}
	m.mu.Unlock()
	m.publish()
}

func accountAuthenticationObservation(at time.Time, state domain.AgentAuthenticationState) domain.AgentAuthenticationObservation {
	switch state {
	case domain.AgentAuthenticationAuthorized:
		return domain.SuccessfulAuthentication(at, state, domain.AgentReadinessReasonAuthorized, "Codex appears signed in.")
	case domain.AgentAuthenticationUnauthorized:
		return domain.SuccessfulAuthentication(at, state, domain.AgentReadinessReasonUnauthorized, "Codex needs authentication.")
	case domain.AgentAuthenticationNotApplicable:
		return domain.SuccessfulAuthentication(at, state, domain.AgentReadinessReasonAuthNotApplicable, "Codex authentication is not required.")
	default:
		return domain.FailedAuthentication(at, domain.AgentReadinessReasonAuthCheckInconclusive, "Authentication check was inconclusive.")
	}
}
