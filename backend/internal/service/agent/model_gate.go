package agent

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// ModelGate adjusts a catalog for facts that change independently of model
// discovery, such as which models the installed agent CLI is too old to use.
// It runs on every read so the durable cache stays the raw discovered list.
type ModelGate func(ctx context.Context, agentID string, catalog ports.AgentModelCatalog) ports.AgentModelCatalog

func (s *Service) gateModels(ctx context.Context, agentID string, catalog ports.AgentModelCatalog) ports.AgentModelCatalog {
	if s.modelGate == nil {
		return catalog
	}
	return s.modelGate(ctx, agentID, catalog)
}
