package apitypes

import (
	"tallpine.eu/tallpine/backend/internal/domain"
)

// EnsureAgentReadinessRequest selects harnesses and the daemon freshness policy.
// An omitted or empty agentIds list selects all supported harnesses.
type EnsureAgentReadinessRequest struct {
	AgentIDs []string                     `json:"agentIds,omitempty"`
	Purpose  domain.AgentReadinessPurpose `json:"purpose" enum:"display,launch"`
}
