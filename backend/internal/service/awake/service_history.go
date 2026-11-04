package awake

import (
	"context"
	"errors"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ClearHistory removes ended sessions while preserving replay protection.
func (s *Service) ClearHistory(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	previous := clone(s.doc)
	kept := []domain.AwakeSession{}
	for _, source := range s.doc.Sessions {
		if source.EndedAt == nil {
			kept = append(kept, source)
		}
	}
	s.doc.Sessions = kept
	if err := s.save(ctx); err != nil {
		s.doc = previous
		return err
	}
	return nil
}
