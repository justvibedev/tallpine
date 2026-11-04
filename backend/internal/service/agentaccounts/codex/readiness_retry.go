package codex

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// DefaultReadinessRetryDelays is the backoff used for retrying failed
// authentication and usage reads; the agent readiness coordinator shares it.
var DefaultReadinessRetryDelays = []time.Duration{15 * time.Second, time.Minute, 5 * time.Minute}

func preserveAuthenticationFailure(current *domain.AgentAuthenticationObservation, failure domain.AgentAuthenticationObservation) {
	domain.PreserveAuthenticationFailure(current, failure)
}
