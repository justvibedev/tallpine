package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// CreateBacklogRun allocates the project's next run ref and inserts the run.
// A second open run in the project is refused with ErrBacklogRunOpen.
func (s *Store) CreateBacklogRun(ctx context.Context, in domain.BacklogRun) (domain.BacklogRun, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return domain.BacklogRun{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	seq, err := q.SelectNextBacklogRunSeq(ctx, string(in.ProjectID))
	if err != nil {
		return domain.BacklogRun{}, fmt.Errorf("allocate backlog run ref: %w", err)
	}
	in.Seq = seq
	params, err := backlogRunInsertParams(in)
	if err != nil {
		return domain.BacklogRun{}, err
	}
	if err := q.InsertBacklogRun(ctx, params); err != nil {
		switch {
		case isSQLiteUnique(err):
			return domain.BacklogRun{}, domain.ErrBacklogRunOpen
		case isSQLiteForeignKey(err):
			return domain.BacklogRun{}, domain.ErrProjectUnknown
		}
		return domain.BacklogRun{}, fmt.Errorf("insert backlog run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.BacklogRun{}, err
	}
	return in, nil
}

// GetBacklogRun returns a run by its project-scoped number. ok is false when unknown.
func (s *Store) GetBacklogRun(ctx context.Context, projectID domain.ProjectID, seq int64) (domain.BacklogRun, bool, error) {
	row, err := s.qr.SelectBacklogRunBySeq(ctx, gen.SelectBacklogRunBySeqParams{ProjectID: string(projectID), Seq: seq})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BacklogRun{}, false, nil
	}
	if err != nil {
		return domain.BacklogRun{}, false, fmt.Errorf("select backlog run: %w", err)
	}
	run, err := backlogRunFromGen(row)
	return run, err == nil, err
}

// ListBacklogRuns returns a project's runs, newest first; openOnly keeps the
// one that has not finished.
func (s *Store) ListBacklogRuns(ctx context.Context, projectID domain.ProjectID, openOnly bool, limit int) ([]domain.BacklogRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	open := int64(0)
	if openOnly {
		open = 1
	}
	rows, err := s.qr.SelectBacklogRuns(ctx, gen.SelectBacklogRunsParams{ProjectID: string(projectID), OpenOnly: open, RowLimit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list backlog runs: %w", err)
	}
	out := make([]domain.BacklogRun, 0, len(rows))
	for _, row := range rows {
		run, err := backlogRunFromGen(row)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}

func backlogRunInsertParams(r domain.BacklogRun) (gen.InsertBacklogRunParams, error) {
	selection := r.Selection
	if selection == nil {
		selection = []string{}
	}
	raw, err := json.Marshal(selection)
	if err != nil {
		return gen.InsertBacklogRunParams{}, err
	}
	return gen.InsertBacklogRunParams{
		ID: r.ID, ProjectID: string(r.ProjectID), Seq: r.Seq, Scope: string(r.Scope),
		SelectionKind: string(r.SelectionKind), Selection: string(raw), Status: string(r.Status),
		PauseReason: r.PauseReason, ResumeAt: timePtrToNullTime(r.ResumeAt), Summary: r.Summary,
		RequestedBy: string(r.RequestedBy), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		StartedAt: timePtrToNullTime(r.StartedAt), EndedAt: timePtrToNullTime(r.EndedAt),
	}, nil
}

func backlogRunFromGen(row gen.BacklogRun) (domain.BacklogRun, error) {
	var selection []string
	if err := json.Unmarshal([]byte(row.Selection), &selection); err != nil {
		return domain.BacklogRun{}, fmt.Errorf("decode backlog run selection: %w", err)
	}
	return domain.BacklogRun{
		ID: row.ID, ProjectID: domain.ProjectID(row.ProjectID), Seq: row.Seq, Scope: domain.BacklogScope(row.Scope),
		SelectionKind: domain.BacklogRunSelection(row.SelectionKind), Selection: selection,
		Status: domain.BacklogRunStatus(row.Status), PauseReason: row.PauseReason, ResumeAt: nullTimeToTimePtr(row.ResumeAt),
		Summary: row.Summary, RequestedBy: domain.BacklogOriginKind(row.RequestedBy),
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, StartedAt: nullTimeToTimePtr(row.StartedAt), EndedAt: nullTimeToTimePtr(row.EndedAt),
	}, nil
}
