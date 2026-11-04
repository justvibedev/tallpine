package sessionmanager

import (
	"context"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// publishProvisionedWorkspace records the worktree on the session row and
// replays staged attachments into it. It reports false after settling the
// failure itself.
func (m *Manager) publishProvisionedWorkspace(ctx context.Context, in asyncChatSpawn, ws ports.WorkspaceInfo, workspaceProject *ports.WorkspaceProjectInfo) bool {
	id := in.record.ID
	stageStarted := time.Now()
	updated, err := m.store.SetSessionProvisionedWorkspace(
		ctx, id, ws.Branch, ws.Path, ws.RepoPath, m.clock())
	if err != nil {
		m.cleanupAsyncChatWorkspace(ctx, id, ws, workspaceProject)
		m.failAsyncChatSpawn(ctx, id, err)
		return false
	}
	if !updated || ctx.Err() != nil {
		m.cleanupAsyncChatWorkspace(ctx, id, ws, workspaceProject)
		if err := ctx.Err(); err != nil {
			m.failAsyncChatSpawn(ctx, id, err)
		}
		return false
	}
	m.logAsyncChatSpawnStage(id, "workspace_publish", stageStarted)
	// A concurrent StageAttachments call must either see this workspace path and
	// write directly into it, or land in canonical storage before this replay.
	// This also projects opening attachments saved before the early response.
	stageStarted = time.Now()
	if err := m.restoreAttachments(ctx, id, ws); err != nil {
		m.failAsyncChatSpawn(ctx, id, wrapSpawnStage(id, ErrSpawnAttachments, err))
		return false
	}
	m.logAsyncChatSpawnStage(id, "attachment_restore", stageStarted)
	return true
}

// launchProvisionedChat starts the chat controller on the published workspace,
// drains the queued opening prompt and marks the session ready. It reports
// false after settling the failure itself.
func (m *Manager) launchProvisionedChat(ctx context.Context, in asyncChatSpawn, ws ports.WorkspaceInfo, workspaceProject *ports.WorkspaceProjectInfo, releaseWorkspaceGate func(), progress *provisionProgress) (domain.SessionRecord, bool) {
	id := in.record.ID
	record, err := m.getRecord(ctx, id)
	if err != nil {
		m.cleanupAsyncChatWorkspace(ctx, id, ws, workspaceProject)
		m.failAsyncChatSpawn(ctx, id, err)
		return domain.SessionRecord{}, false
	}
	if err := ctx.Err(); err != nil {
		m.cleanupAsyncChatWorkspace(ctx, id, ws, workspaceProject)
		m.failAsyncChatSpawn(ctx, id, err)
		return domain.SessionRecord{}, false
	}
	stageStarted := time.Now()
	if _, err := m.launchChatController(ctx, chatSpawn{
		policy:           in.policy,
		cfg:              in.cfg,
		project:          in.project,
		projectKind:      in.projectKind,
		record:           record,
		workspace:        ws,
		workspaceProject: workspaceProject,
		prompt:           in.prompt,
		systemPrompt:     in.systemPrompt,
		systemPromptFile: in.systemPromptFile,
		// The opening prompt is already a queued turn; the drain below delivers
		// it together with anything typed while this was starting.
		promptQueued: true,
		modelNotice:  in.modelNotice,
	}); err != nil {
		m.failAsyncChatSpawn(ctx, id, err)
		return domain.SessionRecord{}, false
	}
	// The agent owns the checkout now; nothing below touches the workspace.
	releaseWorkspaceGate()
	m.logAsyncChatSpawnStage(id, "controller_start", stageStarted)
	stageStarted = time.Now()
	if err := m.chat.DrainChatQueue(ctx, id); err != nil {
		m.failAsyncChatSpawn(ctx, id, wrapSpawnStage(id, ErrSpawnDeliverPrompt, err))
		return domain.SessionRecord{}, false
	}
	m.logAsyncChatSpawnStage(id, "queue_drain", stageStarted)
	stageStarted = time.Now()
	if _, err := m.setProvisionState(ctx, id, domain.SessionProvisionReady, ""); err != nil {
		m.failAsyncChatSpawn(ctx, id, err)
		return domain.SessionRecord{}, false
	}
	m.logAsyncChatSpawnStage(id, "mark_ready", stageStarted)
	progress.complete(ctx, domain.SessionProvisionStepAgent)
	return record, true
}

// retryLiveChatController finishes a retry whose controller is already running:
// it only drains the queue and marks the session ready.
func (m *Manager) retryLiveChatController(ctx context.Context, rec domain.SessionRecord) (RestoreResult, error) {
	if _, err := m.setProvisionState(ctx, rec.ID, domain.SessionProvisionProvisioning, ""); err != nil {
		return RestoreResult{}, err
	}
	progress := m.startProvisionProgress(ctx, rec.ID, []domain.SessionProvisionStepID{domain.SessionProvisionStepAgent})
	if err := m.chat.DrainChatQueue(ctx, rec.ID); err != nil {
		cleanupCtx, cancel := spawnRollbackContext(ctx)
		defer cancel()
		if _, writeErr := m.setProvisionState(cleanupCtx, rec.ID, domain.SessionProvisionFailed, err.Error()); writeErr != nil {
			return RestoreResult{}, errors.Join(err, writeErr)
		}
		return RestoreResult{}, err
	}
	if _, err := m.setProvisionState(ctx, rec.ID, domain.SessionProvisionReady, ""); err != nil {
		cleanupCtx, cancel := spawnRollbackContext(ctx)
		defer cancel()
		if _, writeErr := m.setProvisionState(cleanupCtx, rec.ID, domain.SessionProvisionFailed, err.Error()); writeErr != nil {
			return RestoreResult{}, errors.Join(err, writeErr)
		}
		return RestoreResult{}, err
	}
	progress.complete(ctx, domain.SessionProvisionStepAgent)
	ready, err := m.getRecord(ctx, rec.ID)
	return RestoreResult{Session: ready, Mode: RestoreModeNative}, err
}

// retryResumeChatConversation retries a start whose provider conversation
// already exists by resuming it instead of provisioning a new one.
func (m *Manager) retryResumeChatConversation(ctx context.Context, rec domain.SessionRecord) (RestoreResult, error) {
	var err error
	rec, err = m.setProvisionState(ctx, rec.ID, domain.SessionProvisionProvisioning, "")
	if err != nil {
		return RestoreResult{}, err
	}
	progress := m.startProvisionProgress(ctx, rec.ID, []domain.SessionProvisionStepID{domain.SessionProvisionStepAgent})
	fail := func(cause error) (RestoreResult, error) {
		cleanupCtx, cancel := spawnRollbackContext(ctx)
		defer cancel()
		if _, err := m.setProvisionState(cleanupCtx, rec.ID, domain.SessionProvisionFailed, cause.Error()); err != nil {
			return RestoreResult{}, errors.Join(cause, err)
		}
		return RestoreResult{}, cause
	}
	resumed, err := m.resumeAgentRecordWithPolicy(ctx, "retry start", rec, false)
	if err != nil {
		return fail(err)
	}
	if err := m.chat.DrainChatQueue(ctx, rec.ID); err != nil {
		return fail(err)
	}
	resumed.Session, err = m.setProvisionState(ctx, rec.ID, domain.SessionProvisionReady, "")
	if err != nil {
		return fail(err)
	}
	progress.complete(ctx, domain.SessionProvisionStepAgent)
	resumed.Session, err = m.getRecord(ctx, rec.ID)
	if err != nil {
		return RestoreResult{}, err
	}
	return resumed, nil
}
