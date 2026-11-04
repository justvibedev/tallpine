package store

import (
	"context"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// Automatic cleanup policy and run history (see service/cleanup).

// CleanupRun is one recorded cleanup pass. Report is the pass's JSON report.
type CleanupRun struct {
	ID         int64
	StartedAt  time.Time
	FinishedAt time.Time
	Trigger    string
	Pass       string
	DryRun     bool
	FreedBytes int64
	Report     string
	Error      string
}

func cleanupRunFromRow(row gen.CleanupRun) CleanupRun {
	return CleanupRun{
		ID: row.ID, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt,
		Trigger: row.Trigger, Pass: row.Pass, DryRun: row.DryRun != 0,
		FreedBytes: row.FreedBytes, Report: row.Report, Error: row.Error,
	}
}

func firstCleanupRun(rows []gen.CleanupRun) (CleanupRun, bool) {
	if len(rows) == 0 {
		return CleanupRun{}, false
	}
	return cleanupRunFromRow(rows[0]), true
}

// GetCleanupPolicy reads the stored policy document ("{}" means defaults).
func (s *Store) GetCleanupPolicy(ctx context.Context) (string, time.Time, error) {
	row, err := s.qr.GetCleanupPolicy(ctx)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read cleanup policy: %w", err)
	}
	return row.Policy, row.UpdatedAt, nil
}

// SetCleanupPolicy replaces the stored policy document.
func (s *Store) SetCleanupPolicy(ctx context.Context, policy string, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetCleanupPolicy(ctx, gen.SetCleanupPolicyParams{Policy: policy, UpdatedAt: now}); err != nil {
		return fmt.Errorf("set cleanup policy: %w", err)
	}
	return nil
}

// InsertCleanupRun records a pass and returns its id.
func (s *Store) InsertCleanupRun(ctx context.Context, run CleanupRun) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	id, err := s.qw.InsertCleanupRun(ctx, gen.InsertCleanupRunParams{
		StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, Trigger: run.Trigger, Pass: run.Pass,
		DryRun: boolInt(run.DryRun), FreedBytes: run.FreedBytes, Report: run.Report, Error: run.Error,
	})
	if err != nil {
		return 0, fmt.Errorf("record cleanup run: %w", err)
	}
	return id, nil
}

// ListCleanupRuns returns the most recent passes, newest first.
func (s *Store) ListCleanupRuns(ctx context.Context, limit int64) ([]CleanupRun, error) {
	rows, err := s.qr.ListCleanupRuns(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list cleanup runs: %w", err)
	}
	out := make([]CleanupRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, cleanupRunFromRow(row))
	}
	return out, nil
}

// LatestCleanupRun returns the newest real (or dry) pass.
func (s *Store) LatestCleanupRun(ctx context.Context, dryRun bool) (CleanupRun, bool, error) {
	rows, err := s.qr.LatestCleanupRun(ctx, boolInt(dryRun))
	if err != nil {
		return CleanupRun{}, false, fmt.Errorf("latest cleanup run: %w", err)
	}
	run, ok := firstCleanupRun(rows)
	return run, ok, nil
}

// TrimCleanupRuns keeps the newest keep real (or dry) passes.
func (s *Store) TrimCleanupRuns(ctx context.Context, dryRun bool, keep int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	ids, err := s.qw.CleanupRunTrimCutoff(ctx, gen.CleanupRunTrimCutoffParams{DryRun: boolInt(dryRun), Offset: keep})
	if err != nil {
		return fmt.Errorf("trim cleanup runs: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	if err := s.qw.DeleteCleanupRunsUpTo(ctx, gen.DeleteCleanupRunsUpToParams{DryRun: boolInt(dryRun), ID: ids[0]}); err != nil {
		return fmt.Errorf("trim cleanup runs: %w", err)
	}
	return nil
}
