package store

import (
	"context"
	"database/sql"
	"errors"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// UpdateProjectConfig reads an active project's stored row, lets mutate return
// the next config, and writes only the config column back, all in one
// transaction under writeMu. A concurrent config, settings or permissions write
// is therefore never overwritten with a stale copy, and the row's other columns
// (display name, archive state) are not touched. mutate sees the config as
// stored (env still sealed) and an error from it aborts the write. It reports
// found=false for a missing or archived project.
func (s *Store) UpdateProjectConfig(ctx context.Context, id string, mutate func(domain.ProjectRecord) (domain.ProjectConfig, error)) (domain.ProjectRecord, bool, error) {
	ctx = s.primeEnvKey(ctx, id)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var row domain.ProjectRecord
	var found bool
	err := s.inTx(ctx, "update project config", func(q *gen.Queries) error {
		stored, err := q.GetProject(ctx, domain.ProjectID(id))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		row = projectRowFromGen(stored)
		if !row.ArchivedAt.IsZero() {
			return nil
		}
		next, err := mutate(row)
		if err != nil {
			return err
		}
		config, err := marshalProjectConfig(next)
		if err != nil {
			return err
		}
		rows, err := q.UpdateProjectSettings(ctx, gen.UpdateProjectSettingsParams{ID: domain.ProjectID(id), DisplayName: row.DisplayName, Config: config})
		if err != nil {
			return err
		}
		found = rows > 0
		row.Config = next
		if row.Config.Env, err = s.envCodec.Open(ctx, id, row.Config.Env); err != nil {
			row.Config.Env = nil
		}
		row.Config = s.managed.ApplyProject(row.Config)
		return nil
	})
	return row, found, err
}
