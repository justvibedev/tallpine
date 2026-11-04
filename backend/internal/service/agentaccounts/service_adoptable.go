package agentaccounts

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Account resolves an account by id, the computer's own login included.
func (s *Service) Account(ctx context.Context, id string) (domain.AgentAccount, error) {
	return s.find(ctx, id)
}
