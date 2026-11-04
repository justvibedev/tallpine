package sessionmanager

import (
	"context"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func (m *Manager) getRecord(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("get %s: %w", id, ErrNotFound)
	}
	return rec, nil
}

// PersistChatModel records or clears the ChatUI model override before the next
// prompt routes. A later TUI rebuild reads this session metadata; the update
// leaves conversation history and other session fields untouched.
func (m *Manager) PersistChatModel(ctx context.Context, id domain.SessionID, model string) error {
	want := strings.TrimSpace(model)
	updated, err := m.store.UpdateSessionModel(ctx, id, want)
	if err != nil {
		return fmt.Errorf("persist chat model %s: %w", id, err)
	}
	if !updated {
		return fmt.Errorf("persist chat model %s: %w", id, ErrNotFound)
	}
	return nil
}

// SaveAndTeardownAll captures uncommitted work and tears down every live
// session that has a workspace path. It is the shutdown path for the daemon:
// each session's uncommitted work is stashed into a preserve ref, the ref is
// written to session_worktrees (the "shutdown-saved" marker) BEFORE the
// worktree is force-removed. The DB write is committed before the worktree is
// destroyed so a crash between the two leaves the ref in place and the row
// present; RestoreAll will replay both.
//
// Failures on individual sessions are logged and do not abort the loop.
// ForceDestroy is never called if capture or the DB write did not succeed.
func (m *Manager) SaveAndTeardownAll(ctx context.Context) error {
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("save-teardown-all: list sessions: %w", err)
	}
	for _, rec := range recs {
		if rec.IsTerminated || rec.IsTaskPreparation {
			continue
		}
		if rec.Metadata.WorkspacePath == "" || rec.Metadata.Branch == "" {
			continue
		}
		if err := m.saveAndTeardownOne(ctx, rec); err != nil {
			m.logger.Error("save-teardown-all: session failed, skipping", "sessionID", rec.ID, "error", err)
		}
	}
	return nil
}

// saveAndTeardownOne runs the capture-then-destroy sequence for a single
// session. The DB write (UpsertSessionWorktree) is committed before
// ForceDestroy; if either capture or the DB write fails, ForceDestroy is
// not called.
func (m *Manager) saveAndTeardownOne(ctx context.Context, rec domain.SessionRecord) error {
	// Gate shut this session's scoped shell terminals before either branch
	// below force-removes its worktree. SaveAndTeardownAll only reaches here for
	// a session with a real workspace, so there is always a worktree to protect.
	release, closeErr := m.beginShellTerminalTeardown(ctx, rec.ID)
	if closeErr != nil {
		return fmt.Errorf("save %s: %w", rec.ID, closeErr)
	}
	if release != nil {
		defer release()
	}
	if err := m.terminateNativeSession(ctx, rec); err != nil {
		return fmt.Errorf("save %s: native session: %w", rec.ID, err)
	}
	if err := m.importAttachments(ctx, rec); err != nil {
		return fmt.Errorf("save %s: preserve attachments: %w", rec.ID, err)
	}

	if rows, ok, err := m.workspaceProjectRows(ctx, rec); err != nil {
		return fmt.Errorf("save %s: workspace rows: %w", rec.ID, err)
	} else if ok {
		return m.saveAndTeardownWorkspaceProject(ctx, rec, rows)
	}

	// 1. Capture uncommitted work (ref may be "" for clean worktrees).
	ws := workspaceInfo(rec)
	ref, err := m.workspace.StashUncommitted(ctx, ws)
	if err != nil {
		return fmt.Errorf("save %s: stash: %w", rec.ID, err)
	}

	// 2. Write the shutdown-saved marker to the DB. The row's presence (even
	// with an empty preserved_ref) is what RestoreAll uses to identify sessions
	// saved by this run. This MUST be committed before ForceDestroy.
	row := domain.SessionWorktreeRecord{
		SessionID:    rec.ID,
		RepoName:     domain.RootWorkspaceRepoName,
		Branch:       rec.Metadata.Branch,
		WorktreePath: rec.Metadata.WorkspacePath,
		PreservedRef: ref,
		State:        "removed",
	}
	if err := m.store.UpsertSessionWorktree(ctx, row); err != nil {
		return fmt.Errorf("save %s: upsert worktree row: %w", rec.ID, err)
	}

	// 3. Remove reviewer panes before the worktree disappears, while preserving
	// review rows and native reviewer ids for restore. This is shutdown recovery,
	// not user intent to cancel review history.
	if err := m.teardownReviewerTerminal(ctx, rec.ID); err != nil {
		return fmt.Errorf("save %s: teardown reviewer: %w", rec.ID, err)
	}

	// 4. Mark terminal via the LCM (same path Kill uses).
	if err := m.lcm.MarkTerminated(ctx, rec.ID); err != nil {
		return fmt.Errorf("save %s: mark terminated: %w", rec.ID, err)
	}

	// 5. Runtime teardown (best-effort; same pattern as Kill).
	handle := runtimeHandle(rec.Metadata)
	if handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			m.logger.Warn("save-teardown-all: runtime destroy failed", "sessionID", rec.ID, "error", err)
		}
	}

	// 6. Force-remove the worktree (safe: work is captured in step 1 and the
	// DB write in step 2 is already committed).
	if err := m.workspace.ForceDestroy(ctx, ws); err != nil {
		m.logger.Warn("save-teardown-all: force destroy failed", "sessionID", rec.ID, "error", err)
	} else {
		m.cleanupAgentWorkspace(ctx, rec, ws.Path)
	}
	return nil
}
