package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// SetSessionProvisionedWorkspace records the worktree as soon as it exists,
// ahead of the controller commit that writes the rest of the row.
func (s *Store) SetSessionProvisionedWorkspace(
	ctx context.Context,
	id domain.SessionID,
	branch, workspacePath, workspaceRepoPath string,
	now time.Time,
) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionProvisionedWorkspace(ctx, gen.SetSessionProvisionedWorkspaceParams{
		Branch:            branch,
		WorkspacePath:     workspacePath,
		WorkspaceRepoPath: workspaceRepoPath,
		UpdatedAt:         now,
		ID:                id,
	})
	if err != nil {
		return false, fmt.Errorf("record provisioned workspace for %s: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionTaskBranch records a background task branch rename on the session
// and its repository rows together. repoBranches maps each repository row to
// the branch its checkout holds, which after a failed undo can differ.
func (s *Store) SetSessionTaskBranch(ctx context.Context, id domain.SessionID, expectedBranch, branch string, repoBranches map[string]string, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var rows int64
	err := s.inTx(ctx, fmt.Sprintf("record task branch for %s", id), func(q *gen.Queries) error {
		var err error
		rows, err = q.SetSessionTaskBranch(ctx, gen.SetSessionTaskBranchParams{
			ID: id, ExpectedBranch: expectedBranch, Branch: branch, UpdatedAt: now,
		})
		for repo, repoBranch := range repoBranches {
			if err != nil || rows == 0 {
				return err
			}
			err = q.SetSessionWorktreeBranch(ctx, gen.SetSessionWorktreeBranchParams{SessionID: id, RepoName: repo, Branch: repoBranch})
		}
		return err
	})
	return err == nil && rows > 0, err
}

// SetSessionBranchNaming marks whether a task's generated branch name is still
// on the way.
func (s *Store) SetSessionBranchNaming(ctx context.Context, id domain.SessionID, naming bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetSessionBranchNaming(ctx, gen.SetSessionBranchNamingParams{ID: id, BranchNaming: naming}); err != nil {
		return fmt.Errorf("record branch naming for %s: %w", id, err)
	}
	return nil
}

// ClearSessionBranchNaming ends every branch naming a restart interrupted.
func (s *Store) ClearSessionBranchNaming(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.ClearSessionBranchNaming(ctx); err != nil {
		return fmt.Errorf("clear interrupted branch naming: %w", err)
	}
	return nil
}

// SetTaskPreparationBase records the immutable base only while the row is hidden.
func (s *Store) SetTaskPreparationBase(ctx context.Context, id domain.SessionID, baseSHA, baseRef string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetTaskPreparationBase(ctx, gen.SetTaskPreparationBaseParams{
		DiffBaseSha: baseSHA,
		DiffBaseRef: baseRef,
		ID:          id,
	})
	if err != nil {
		return false, fmt.Errorf("record task preparation base for %s: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionProvisionState publishes an asynchronous Chat spawn's progress. It
// writes only these two fields: the background start races the controller
// commit, which owns the rest of the row.
func (s *Store) SetSessionProvisionState(
	ctx context.Context,
	id domain.SessionID,
	state domain.SessionProvisionState,
	provisionError string,
	now time.Time,
) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionProvisionState(ctx, gen.SetSessionProvisionStateParams{
		ProvisionState: state.WithDefault(),
		ProvisionError: provisionError,
		UpdatedAt:      now,
		ID:             id,
	})
	if err != nil {
		return false, fmt.Errorf("set provision state for %s: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionProvisionSteps publishes start progress and the pre-launch setup
// checkpoint. Like SetSessionProvisionState it writes only its own column.
func (s *Store) SetSessionProvisionSteps(
	ctx context.Context,
	id domain.SessionID,
	steps []domain.SessionProvisionStep,
	now time.Time,
) error {
	raw, err := json.Marshal(steps)
	if err != nil {
		return fmt.Errorf("encode provision steps for %s: %w", id, err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.qw.SetSessionProvisionSteps(ctx, gen.SetSessionProvisionStepsParams{
		ProvisionSteps: string(raw),
		UpdatedAt:      now,
		ID:             id,
	}); err != nil {
		return fmt.Errorf("set provision steps for %s: %w", id, err)
	}
	return nil
}

// SetSessionBranchState records the branch-state reconcile's latest facts. It writes
// only its own column, so a stale session read cannot replay other fields.
func (s *Store) SetSessionBranchState(ctx context.Context, id domain.SessionID, state domain.SessionBranchState) (bool, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return false, fmt.Errorf("encode branch state for %s: %w", id, err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionBranchState(ctx, gen.SetSessionBranchStateParams{
		BranchState: string(raw),
		ID:          id,
	})
	if err != nil {
		return false, fmt.Errorf("set branch state for %s: %w", id, err)
	}
	return rows > 0, nil
}

// PromoteTaskPreparation makes a hidden speculative row visible without
// touching workspace facts that may be published by the preparation goroutine.
func (s *Store) PromoteTaskPreparation(ctx context.Context, id domain.SessionID, rec domain.SessionRecord) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	activity := normalActivity(rec.Activity, rec.UpdatedAt)
	rows, err := s.qw.PromoteTaskPreparation(ctx, gen.PromoteTaskPreparationParams{
		IssueID:            rec.IssueID,
		Kind:               rec.Kind,
		Harness:            rec.Harness,
		AutoReviewEnabled:  rec.AutoReviewEnabled,
		DisplayName:        rec.DisplayName,
		ActivityState:      activity.State,
		ActivityLastAt:     activity.LastActivityAt,
		SessionMode:        domain.NormalizeSessionMode(rec.Mode),
		Model:              rec.Metadata.Model,
		Effort:             rec.Metadata.Effort,
		SessionPermissions: string(rec.Metadata.Permissions),
		RequestedOutput:    string(rec.RequestedOutput),
		Role:               string(rec.Role),
		CreatedAt:          rec.CreatedAt,
		UpdatedAt:          rec.UpdatedAt,
		AutoInjectReview:   rec.AutoInjectReview,
		AutoInjectCI:       rec.AutoInjectCI,
		ProvisionState:     rec.ProvisionState.WithDefault(),
		ClientRequestID:    rec.ClientRequestID,
		ClientRequestHash:  rec.ClientRequestHash,
		ID:                 id,
	})
	if err != nil {
		return false, fmt.Errorf("promote task preparation %s: %w", id, err)
	}
	return rows > 0, nil
}

// DeleteTaskPreparation removes only a row that has not been claimed.
func (s *Store) DeleteTaskPreparation(ctx context.Context, id domain.SessionID) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin delete task preparation %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
DELETE FROM change_log
WHERE session_id = ?
  AND EXISTS (SELECT 1 FROM sessions WHERE id = ? AND is_task_preparation = 1)`, id, id); err != nil {
		return false, fmt.Errorf("delete task preparation %s change log: %w", id, err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ? AND is_task_preparation = 1`, id)
	if err != nil {
		return false, fmt.Errorf("delete task preparation %s: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete task preparation %s rows affected: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit delete task preparation %s: %w", id, err)
	}
	return rows > 0, nil
}
