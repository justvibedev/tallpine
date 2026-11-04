package domain

import "time"

// SuccessfulAuthentication is a fresh observation of an authentication check
// that completed with the given state.
func SuccessfulAuthentication(at time.Time, state AgentAuthenticationState, code, reason string) AgentAuthenticationObservation {
	return AgentAuthenticationObservation{State: state, Freshness: AgentReadinessFresh, CheckedAt: &at, AttemptedAt: &at, ReasonCode: code, Reason: reason}
}

// FailedAuthentication is a stale, unknown observation of an authentication
// check that could not complete.
func FailedAuthentication(at time.Time, code, reason string) AgentAuthenticationObservation {
	return AgentAuthenticationObservation{State: AgentAuthenticationUnknown, Freshness: AgentReadinessStale, AttemptedAt: &at, ReasonCode: code, Reason: reason}
}

// PreserveAuthenticationFailure replaces current with failure but keeps the
// last known state and check time.
func PreserveAuthenticationFailure(current *AgentAuthenticationObservation, failure AgentAuthenticationObservation) {
	state, checkedAt := current.State, current.CheckedAt
	*current = failure
	current.State = state
	current.CheckedAt = checkedAt
}
