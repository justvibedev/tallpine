package agent

import (
	"context"
	"errors"
	"slices"
	"time"

	"tallpine.eu/tallpine/backend/internal/backoff"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (c *readinessCoordinator) runCheck(id string, purpose domain.AgentReadinessPurpose, needed readinessInvalidation, presenceOnly, includeAuthentication bool, call *readinessCall, started time.Time) {
	item, ok := c.freshAgent(id)
	install := domain.AgentInstallationObservation{}
	auth := domain.AgentAuthenticationObservation{}
	installFailed := false
	authFailed := false
	previousInstallState := domain.AgentInstallationUnknown
	if needed&readinessInvalidateInstallation != 0 {
		c.mu.Lock()
		previousInstallState = c.entries[id].snapshot.Installation.State
		c.mu.Unlock()
	}
	if !ok {
		now := c.now()
		install = failedInstallation(now, domain.AgentReadinessReasonInstallCheckFailed, "Installation check failed.")
		installFailed = true
	} else {
		if needed&readinessInvalidateInstallation != 0 {
			install, installFailed = c.checkInstallation(item, presenceOnly)
			if includeAuthentication && !presenceOnly && !installFailed && install.State == domain.AgentInstallationInstalled && previousInstallState != domain.AgentInstallationInstalled {
				needed |= readinessInvalidateAuthentication
			}
		}
		if !installFailed {
			state := install.State
			if needed&readinessInvalidateInstallation == 0 {
				c.mu.Lock()
				state = c.entries[id].snapshot.Installation.State
				c.mu.Unlock()
			}
			switch state {
			case domain.AgentInstallationInstalled:
				if needed&readinessInvalidateAuthentication != 0 {
					auth, authFailed = c.checkAuthentication(item, purpose)
				}
			case domain.AgentInstallationNotInstalled:
				auth = skippedAuthentication(c.now())
			case domain.AgentInstallationUnknown:
				auth = inconclusiveAuthentication(c.now())
			}
		}
	}

	recovered := false
	c.mu.Lock()
	entry := c.entries[id]
	if needed&readinessInvalidateInstallation != 0 {
		if installFailed {
			preserveInstallationFailure(&entry.snapshot.Installation, install)
		} else {
			entry.snapshot.Installation = install
			if entry.installVersion == call.installVersion &&
				(!presenceOnly || install.ReasonCode != domain.AgentReadinessReasonInstallIdentityPending) {
				entry.invalidated &^= readinessInvalidateInstallation
			}
		}
	}
	if installFailed {
		entry.snapshot.Authentication.Freshness = domain.AgentReadinessStale
	} else if auth.ReasonCode != "" {
		if authFailed {
			preserveAuthenticationFailure(&entry.snapshot.Authentication, auth)
		} else {
			recovered = entry.snapshot.Authentication.State == domain.AgentAuthenticationUnauthorized &&
				(auth.State == domain.AgentAuthenticationAuthorized || auth.State == domain.AgentAuthenticationConfigured)
			entry.snapshot.Authentication = auth
			if entry.authVersion == call.authVersion {
				entry.invalidated &^= readinessInvalidateAuthentication
			}
		}
	}
	entry.checking = 0
	failureCode := ""
	if installFailed || authFailed {
		entry.failures++
		delay := backoff.Table(c.retryDelays, entry.failures)
		entry.nextRetryAt = c.now().Add(delay)
		entry.retryGeneration++
		generation := entry.retryGeneration
		if installFailed {
			failureCode = entry.snapshot.Installation.ReasonCode
		} else {
			failureCode = entry.snapshot.Authentication.ReasonCode
		}
		go c.scheduleRetry(id, generation, delay)
	} else {
		entry.failures = 0
		entry.nextRetryAt = time.Time{}
		entry.retryGeneration++
	}
	snapshot := c.snapshotLocked(entry, purpose)
	nextRetry := entry.nextRetryAt
	duration := c.now().Sub(started)
	delete(c.calls, call.key)
	close(call.done)
	c.mu.Unlock()
	c.logDecision(id, purpose, "new_check", duration, snapshot, failureCode, nextRetry)
	if recovered && c.onAuthRecovered != nil {
		go c.onAuthRecovered(id)
	}
}

func (c *readinessCoordinator) checkInstallation(item ports.HarnessAgent, presenceOnly bool) (domain.AgentInstallationObservation, bool) {
	attempted := c.now()
	ctx, cancel := context.WithTimeout(c.ctx, c.installTimeout)
	defer cancel()
	var path string
	var err error
	if resolver, ok := item.Agent.(ports.AgentBinaryPresenceResolver); presenceOnly && ok {
		path, err = resolver.ResolveBinaryPresence(ctx)
	} else if resolver, ok := item.Agent.(ports.AgentBinaryResolver); ok {
		path, err = resolver.ResolveBinary(ctx)
	} else {
		return successfulInstallation(attempted, domain.AgentInstallationUnknown, domain.AgentReadinessReasonInstallCheckUnsupported, "Installation checks are not supported for this harness."), false
	}
	if errors.Is(err, ports.ErrAgentBinaryIdentityUnknown) {
		return successfulInstallation(attempted, domain.AgentInstallationUnknown, domain.AgentReadinessReasonInstallIdentityPending, item.Name+" is present but its identity has not been confirmed."), false
	}
	if err == nil && path != "" {
		return successfulInstallation(attempted, domain.AgentInstallationInstalled, domain.AgentReadinessReasonInstalled, item.Name+" is installed."), false
	}
	if errors.Is(err, ports.ErrAgentBinaryNotFound) {
		return successfulInstallation(attempted, domain.AgentInstallationNotInstalled, domain.AgentReadinessReasonNotInstalled, item.Name+" is not installed."), false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return failedInstallation(attempted, domain.AgentReadinessReasonInstallCheckTimeout, "Installation check timed out."), true
	}
	return failedInstallation(attempted, domain.AgentReadinessReasonInstallCheckFailed, "Installation check failed."), true
}

func (c *readinessCoordinator) checkAuthentication(item ports.HarnessAgent, purpose domain.AgentReadinessPurpose) (observation domain.AgentAuthenticationObservation, failed bool) {
	var diagnostics []domain.AgentAuthDiagnostic
	evidenceCtx, evidence := ports.WithAuthEvidence(c.ctx)
	defer func() {
		observation.Diagnostics = append(observation.Diagnostics, diagnostics...)
		observation.Diagnostics = append(observation.Diagnostics, evidence.Diagnostics()...)
		if source := evidence.Identity().Sanitized(); source != nil && source.Source != "" {
			step := domain.NewAgentAuthDiagnostic("selected_credential", "credential_present")
			step.Source, step.Provider = source.Source, source.Provider
			observation.Diagnostics = append(observation.Diagnostics, step)
		}
		completeAuthenticationDiagnostics(&observation)
	}()
	attempted := c.now()
	if c.authenticationCheck != nil {
		ctx, cancel := context.WithTimeout(evidenceCtx, c.authTimeout)
		observation, handled := c.authenticationCheck(ctx, string(item.Harness), purpose)
		cancel()
		if handled {
			failed := isReadinessFailureCode(observation.ReasonCode)
			return observation, failed
		}
	}
	checker, ok := item.Agent.(ports.AgentAuthChecker)
	if !ok {
		return successfulAuthentication(attempted, domain.AgentAuthenticationUnknown, domain.AgentReadinessReasonAuthCheckUnsupported, "Authentication checks are not supported for this harness."), false
	}
	ctx, cancel := context.WithTimeout(evidenceCtx, c.authTimeout)
	defer cancel()
	var status ports.AgentAuthStatus
	var account domain.AgentAccountIdentity
	var err error
	var launchEnv map[string]string
	if c.authenticationEnv != nil {
		launchEnv = c.authenticationEnv(ctx, string(item.Harness))
	}
	envChecker, hasEnvChecker := item.Agent.(ports.AgentAuthEnvChecker)
	if hasEnvChecker && len(launchEnv) > 0 {
		status, account, diagnostics, err = envChecker.AuthStatusWithDiagnosticsEnv(ctx, launchEnv)
	} else if authChecker, ok := item.Agent.(ports.AgentAuthDiagnosticsChecker); ok {
		status, account, diagnostics, err = authChecker.AuthStatusWithDiagnostics(ctx)
	} else {
		status, err = checker.AuthStatus(ctx)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return failedAuthentication(attempted, domain.AgentReadinessReasonAuthCheckTimeout, "Authentication check timed out."), true
		}
		return failedAuthentication(attempted, domain.AgentReadinessReasonAuthCheckFailed, "Authentication check failed."), true
	}
	// What the adapter reported about the account, completed by the evidence
	// it recorded for the decision (an environment variable, file or command).
	account = account.Merge(evidence.Identity())
	switch status {
	case ports.AgentAuthStatusAuthorized:
		observation := successfulAuthentication(attempted, domain.AgentAuthenticationAuthorized, domain.AgentReadinessReasonAuthorized, item.Name+" appears signed in.")
		observation.Account = account.Sanitized()
		return observation, false
	case ports.AgentAuthStatusUnauthorized:
		return successfulAuthentication(attempted, domain.AgentAuthenticationUnauthorized, domain.AgentReadinessReasonUnauthorized, item.Name+" needs authentication."), false
	case ports.AgentAuthStatusConfigured:
		// A definite observation — a credential exists — but not a verified
		// one. It is recorded as a success so the coordinator does not retry
		// it as a failure, and it derives to unknown readiness, not ready.
		reason := item.Name + " has credentials configured, but Tallpine could not verify them."
		if slices.ContainsFunc(diagnostics, func(d domain.AgentAuthDiagnostic) bool { return d.Code == "token_renewal_pending" }) {
			reason = item.Name + " is signed in; its access token renews the next time it runs."
		}
		observation := successfulAuthentication(attempted, domain.AgentAuthenticationConfigured, domain.AgentReadinessReasonAuthConfigured, reason)
		observation.Account = account.Sanitized()
		return observation, false
	default:
		return failedAuthentication(attempted, domain.AgentReadinessReasonAuthCheckInconclusive, "Authentication check was inconclusive."), true
	}
}

func successfulInstallation(at time.Time, state domain.AgentInstallationState, code, reason string) domain.AgentInstallationObservation {
	return domain.AgentInstallationObservation{State: state, Freshness: domain.AgentReadinessFresh, CheckedAt: timePtr(at), AttemptedAt: timePtr(at), ReasonCode: code, Reason: reason}
}

func failedInstallation(at time.Time, code, reason string) domain.AgentInstallationObservation {
	return domain.AgentInstallationObservation{State: domain.AgentInstallationUnknown, Freshness: domain.AgentReadinessStale, AttemptedAt: timePtr(at), ReasonCode: code, Reason: reason}
}

func successfulAuthentication(at time.Time, state domain.AgentAuthenticationState, code, reason string) domain.AgentAuthenticationObservation {
	return domain.SuccessfulAuthentication(at, state, code, reason)
}

func failedAuthentication(at time.Time, code, reason string) domain.AgentAuthenticationObservation {
	return domain.FailedAuthentication(at, code, reason)
}

func skippedAuthentication(at time.Time) domain.AgentAuthenticationObservation {
	return successfulAuthentication(at, domain.AgentAuthenticationUnknown, domain.AgentReadinessReasonAuthSkippedNotInstalled, "Authentication was skipped because the harness is not installed.")
}

func inconclusiveAuthentication(at time.Time) domain.AgentAuthenticationObservation {
	return successfulAuthentication(at, domain.AgentAuthenticationUnknown, domain.AgentReadinessReasonAuthCheckInconclusive, "Authentication could not be checked until installation is known.")
}

func preserveInstallationFailure(current *domain.AgentInstallationObservation, failure domain.AgentInstallationObservation) {
	state, checkedAt := current.State, current.CheckedAt
	*current = failure
	current.State = state
	current.CheckedAt = checkedAt
}

func preserveAuthenticationFailure(current *domain.AgentAuthenticationObservation, failure domain.AgentAuthenticationObservation) {
	domain.PreserveAuthenticationFailure(current, failure)
}

func timePtr(value time.Time) *time.Time {
	copied := value
	return &copied
}
