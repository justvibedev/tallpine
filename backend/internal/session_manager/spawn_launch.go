package sessionmanager

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// spawnCreateWorkspace adopts the prepared worktree or creates one.
func (m *Manager) spawnCreateWorkspace(ctx context.Context, st *spawnState) *spawnExit {
	prep := st.prep
	id := st.id
	var err error
	var ws ports.WorkspaceInfo
	var workspaceProject *ports.WorkspaceProjectInfo
	if prep != nil {
		ws, workspaceProject, err = m.awaitTaskPreparation(ctx, prep)
		if err != nil && ctx.Err() != nil {
			cleanupCtx, cancel := spawnRollbackContext(ctx)
			m.discardClaimedTaskPreparation(cleanupCtx, prep)
			cancel()
			m.cleanupArtifactDir(id)
			return spawnFail(wrapSpawnStage(id, ErrWorkspaceCreate, err))
		}
	}
	if ws.Path == "" {
		baseRefs := m.refreshDefaultBranchesBestEffort(ctx, st.project)
		ws, workspaceProject, err = m.createSessionWorkspace(ctx, st.project, st.cfg, id, st.branch, baseRefs)
	}
	if err != nil {
		if ws.Path != "" {
			m.rollbackSeedSpawnWorkspace(ctx, st.rec, ws, workspaceProject, prep != nil, false)
		} else {
			// No worktree exists, so the seed row can be discarded.
			m.rollbackSpawnSeedRowAfterFailure(ctx, id)
			m.cleanupArtifactDir(id)
		}
		return spawnFail(wrapSpawnStage(id, ErrWorkspaceCreate, err))
	}
	st.ws = ws
	st.workspaceProject = workspaceProject
	return nil
}

// spawnProvision provisions the worktree and writes pasted attachments into it.
func (m *Manager) spawnProvision(ctx context.Context, st *spawnState) *spawnExit {
	cfg := &st.cfg
	id := st.id
	ws := st.ws
	// Per-project workspace provisioning: symlink shared files, then run any
	// post-create commands (e.g. `pnpm install`) before the agent launches.
	if err := m.provisionWorkspace(ctx, st.project, ws.Path); err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, st.rec, ws, st.workspaceProject, false, false)
		return spawnFail(wrapSpawnStage(id, ErrWorkspaceProvision, err))
	}
	// Setup has overlapped the naming call; settle the branch before launch.
	ws, st.workspaceProject, st.taskName = m.settleTaskBranch(ctx, st.rec, st.cfg.Prompt, ws, st.workspaceProject, st.taskName)
	st.ws = ws

	// CLI agents receive the prompt as text and cannot consume inline binary
	// data, so any pasted/dropped images are written into the worktree and
	// referenced by path in the prompt. Done after provisioning (so the worktree
	// exists) and before the launch command is built (so the references reach
	// the agent).
	if len(cfg.Attachments) > 0 {
		if err := m.workspace.AddExclude(ctx, ws, "/"+attachmentsDir+"/"); err != nil {
			m.rollbackSeedSpawnWorkspace(ctx, st.rec, ws, st.workspaceProject, false, false)
			return spawnFail(wrapSpawnStage(id, ErrSpawnAttachments, err))
		}
		refs, err := m.writeSpawnAttachments(ctx, id, ws.Path, cfg.Attachments)
		if err != nil {
			m.rollbackSeedSpawnWorkspace(ctx, st.rec, ws, st.workspaceProject, false, false)
			return spawnFail(wrapSpawnStage(id, ErrSpawnAttachments, err))
		}
		st.prompt = appendAttachmentReferences(st.prompt, refs)
	}
	return nil
}

// spawnLaunchChat launches the Chat controller and finishes a Chat spawn.
func (m *Manager) spawnLaunchChat(ctx context.Context, st *spawnState) *spawnExit {
	cfg := &st.cfg
	id := st.id
	rec, err := m.launchChatController(ctx, chatSpawn{
		policy:           st.policy,
		cfg:              *cfg,
		project:          st.project,
		projectKind:      st.projectKind,
		record:           st.rec,
		workspace:        st.ws,
		workspaceProject: st.workspaceProject,
		prompt:           st.prompt,
		systemPrompt:     st.systemPrompt,
		systemPromptFile: st.systemPromptFile,
	})
	st.rec = rec
	if err != nil {
		return spawnFail(err)
	}
	if cfg.AutomationRunID != nil {
		if err := m.markAutomationLaunchCompleted(ctx, id); err != nil {
			return spawnFail(wrapSpawnStage(id, ErrSpawnCommit, err))
		}
		rec, err = m.getRecord(ctx, id)
		if err != nil {
			return spawnFail(err)
		}
		st.rec = rec
	}
	if cfg.ClientRequestID != "" {
		if err := m.store.CommitClientRequestSession(ctx, id); err != nil {
			return spawnFail(wrapSpawnStage(id, ErrSpawnCommit, err))
		}
	}
	st.namingHandedOff = true
	m.nameTaskBranchLater(ctx, st.rec, st.ws, st.workspaceProject, st.taskName, st.cancelNaming)
	return &spawnExit{rec: st.rec, promptBytes: st.promptBytes, systemPromptBytes: st.systemPromptBytes}
}
