package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ErrWorkspaceHoldsWork reports a session worktree with uncommitted changes or
// commits that never landed in the project base. Callers that would destroy
// the worktree outright refuse instead.
var ErrWorkspaceHoldsWork = errors.New("session: workspace holds uncommitted or unpushed work")

// observeSettled observes the session's worktree. exists is false when the
// session has no worktree on disk (nothing to protect). It returns
// ErrWorkspaceHoldsWork, wrapped with what was found, for uncommitted changes
// (tracked or untracked) or a HEAD that moved past the creation commit without
// landing in the project base. A worktree that cannot be observed fails closed.
func (m *Manager) observeSettled(ctx context.Context, rec domain.SessionRecord) (obs ports.WorkspaceObservation, exists bool, err error) {
	path := strings.TrimSpace(rec.Metadata.WorkspacePath)
	if path == "" {
		return obs, false, nil
	}
	if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
		return obs, false, nil
	}
	observer, ok := m.workspace.(ports.WorkspaceObserver)
	if !ok {
		return obs, true, fmt.Errorf("check workspace %s: workspace adapter cannot observe worktrees", rec.ID)
	}
	info := workspaceInfo(rec)
	info.BaseSHA = rec.Metadata.DiffBaseSHA
	obs, err = observer.ObserveWorkspace(ctx, info)
	if err != nil {
		return obs, true, fmt.Errorf("check workspace %s: %w", rec.ID, err)
	}
	if obs.Dirty || obs.Staged || obs.Untracked {
		return obs, true, fmt.Errorf("%w: %d uncommitted path(s)", ErrWorkspaceHoldsWork, len(obs.Changes))
	}
	if base := strings.TrimSpace(rec.Metadata.DiffBaseSHA); base != "" && obs.HeadSHA != base && !obs.Landed {
		return obs, true, fmt.Errorf("%w: branch %s has commits that are not in the base branch", ErrWorkspaceHoldsWork, obs.Branch)
	}
	return obs, true, nil
}

// CheckWorkspaceSettled reports ErrWorkspaceHoldsWork when the session's
// worktree holds unsaved work, whether or not the session is terminated. A
// session with no worktree on disk is settled.
func (m *Manager) CheckWorkspaceSettled(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return fmt.Errorf("check workspace %s: %w", id, err)
	}
	if !ok {
		return nil
	}
	_, _, err = m.observeSettled(ctx, rec)
	return err
}

// RemoveSettledWorkspace deletes the session's worktree and branch for good,
// after confirming the worktree holds no unsaved work. The check is repeated
// here, right before teardown, so work committed after an earlier check (by an
// agent that has since been stopped) is still refused. The force-removal is only
// reached once the check passed. The branch goes too when it never moved or has
// landed in the base; one with unlanded commits is refused above.
func (m *Manager) RemoveSettledWorkspace(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	obs, exists, err := m.observeSettled(ctx, rec)
	if err != nil {
		return err
	}
	info := workspaceInfo(rec)
	if exists {
		if err := m.workspace.ForceDestroy(ctx, info); err != nil {
			return fmt.Errorf("remove workspace %s: %w", id, err)
		}
	}
	cleaner, ok := m.workspace.(ports.WorkspacePreparationBranchCleaner)
	if !ok {
		return nil
	}
	// DeletePreparedBranch deletes only a branch whose tip is BaseSHA: the
	// creation commit, or the observed tip when it had landed in the base.
	info.BaseSHA = rec.Metadata.DiffBaseSHA
	if obs.Landed && obs.HeadSHA != "" {
		info.BaseSHA = obs.HeadSHA
	}
	return cleaner.DeletePreparedBranch(ctx, info)
}
