package codex

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func (s *Service) structuredCodexAuthentication(ctx context.Context, agentID string, purpose domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, bool) {
	observation, _, handled := s.structuredCodexAuthenticationRecord(ctx, agentID, purpose)
	return observation, handled
}
