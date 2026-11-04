package store

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// AppendSettingsChanges writes changes to the settings change log in one transaction.
func (s *Store) AppendSettingsChanges(ctx context.Context, changes []domain.SettingsChange) error {
	if len(changes) == 0 {
		return nil
	}
	return s.inTx(ctx, "append settings changes", func(q *gen.Queries) error {
		for _, c := range changes {
			if err := q.InsertSettingsChange(ctx, gen.InsertSettingsChangeParams{
				ChangedAt: c.ChangedAt, Actor: c.Actor, ProjectID: string(c.ProjectID),
				SettingKey: c.Key, OldValue: c.OldValue, NewValue: c.NewValue,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListSettingsChanges returns the newest changes of a project ("" for app-wide
// settings), newest first.
func (s *Store) ListSettingsChanges(ctx context.Context, project domain.ProjectID, limit int) ([]domain.SettingsChange, error) {
	rows, err := s.qr.ListSettingsChanges(ctx, gen.ListSettingsChangesParams{ProjectID: string(project), Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]domain.SettingsChange, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.SettingsChange{
			ID: r.ID, ChangedAt: r.ChangedAt, Actor: r.Actor, ProjectID: domain.ProjectID(r.ProjectID),
			Key: r.SettingKey, OldValue: r.OldValue, NewValue: r.NewValue,
		})
	}
	return out, nil
}
