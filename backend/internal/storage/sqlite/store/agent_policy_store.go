package store

import (
	"context"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// AgentPolicy returns the stored user layer of the agent policy as JSON.
func (s *Store) AgentPolicy(ctx context.Context) ([]byte, error) {
	raw, err := s.qr.GetAgentPolicy(ctx)
	if err != nil {
		return nil, fmt.Errorf("read agent policy: %w", err)
	}
	return []byte(raw), nil
}

// SetAgentPolicy stores the user layer of the agent policy.
func (s *Store) SetAgentPolicy(ctx context.Context, data []byte, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetAgentPolicy(ctx, gen.SetAgentPolicyParams{AgentPolicy: string(data), UpdatedAt: now}); err != nil {
		return fmt.Errorf("set agent policy: %w", err)
	}
	return nil
}
