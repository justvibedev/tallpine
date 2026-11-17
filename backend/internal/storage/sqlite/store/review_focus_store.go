package store

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// SetReviewRunFocus records a run's one-off review focus and its author: a
// trigger that restarts a running pass gives that pass its own focus, so a
// later retry of the pass reads the focus it was relaunched with.
func (s *Store) SetReviewRunFocus(ctx context.Context, id, focus string, author domain.ReviewFocusAuthor) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.qw.SetReviewRunFocus(ctx, gen.SetReviewRunFocusParams{Focus: focus, FocusAuthor: string(author), ID: id}); err != nil {
		return fmt.Errorf("set review run %s focus: %w", id, err)
	}
	return nil
}
