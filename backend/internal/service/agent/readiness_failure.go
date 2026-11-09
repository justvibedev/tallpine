package agent

import "tallpine.eu/tallpine/backend/internal/domain"

func readinessFailureCategory(snapshot domain.AgentReadinessSnapshot) string {
	if isReadinessFailureCode(snapshot.Installation.ReasonCode) {
		return snapshot.Installation.ReasonCode
	}
	if isReadinessFailureCode(snapshot.Authentication.ReasonCode) {
		return snapshot.Authentication.ReasonCode
	}
	return ""
}

func isReadinessFailureCode(code string) bool {
	switch code {
	case domain.AgentReadinessReasonInstallCheckTimeout,
		domain.AgentReadinessReasonInstallCheckFailed,
		domain.AgentReadinessReasonAuthCheckInconclusive,
		domain.AgentReadinessReasonAuthCheckTimeout,
		domain.AgentReadinessReasonAuthCheckFailed:
		return true
	default:
		return false
	}
}
