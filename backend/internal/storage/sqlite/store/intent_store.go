package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// InsertOrchestratorIntent persists one intent; an unknown project reports
// domain.ErrProjectUnknown.
func (s *Store) InsertOrchestratorIntent(ctx context.Context, in domain.OrchestratorIntent) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.qw.InsertOrchestratorIntent(ctx, gen.InsertOrchestratorIntentParams{
		ID: in.ID, ProjectID: string(in.ProjectID), Text: in.Text, CreatedBy: in.CreatedBy, CreatedAt: in.CreatedAt,
	})
	if err != nil {
		if isSQLiteForeignKey(err) {
			return domain.ErrProjectUnknown
		}
		return fmt.Errorf("insert orchestrator intent %s: %w", in.ID, err)
	}
	return nil
}

// SelectOrchestratorIntents lists a project's intents oldest first; open ones
// only unless includeDone.
func (s *Store) SelectOrchestratorIntents(ctx context.Context, project domain.ProjectID, includeDone bool) ([]domain.OrchestratorIntent, error) {
	var all int64
	if includeDone {
		all = 1
	}
	rows, err := s.qr.SelectOrchestratorIntents(ctx, gen.SelectOrchestratorIntentsParams{ProjectID: string(project), IncludeDone: all})
	if err != nil {
		return nil, fmt.Errorf("select orchestrator intents for project %s: %w", project, err)
	}
	out := make([]domain.OrchestratorIntent, 0, len(rows))
	for _, r := range rows {
		in := domain.OrchestratorIntent{ID: r.ID, ProjectID: domain.ProjectID(r.ProjectID), Text: r.Text, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt}
		if r.DoneAt.Valid {
			t := r.DoneAt.Time
			in.DoneAt = &t
		}
		out = append(out, in)
	}
	return out, nil
}

// MarkOrchestratorIntentDone clears an open intent of the project, reporting
// whether one was open.
func (s *Store) MarkOrchestratorIntentDone(ctx context.Context, project domain.ProjectID, id string, at time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.MarkOrchestratorIntentDone(ctx, gen.MarkOrchestratorIntentDoneParams{
		DoneAt: sql.NullTime{Time: at, Valid: true}, ID: id, ProjectID: string(project),
	})
	if err != nil {
		return false, fmt.Errorf("mark orchestrator intent %s done: %w", id, err)
	}
	return n > 0, nil
}
