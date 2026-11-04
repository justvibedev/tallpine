package sessionmanager

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// spawnPrepareWorker builds the worker's environment and launch command and
// registers the launch with the lifecycle manager.
func (m *Manager) spawnPrepareWorker(ctx context.Context, st *spawnState) *spawnExit {
	cfg := &st.cfg
	id := st.id
	ws := st.ws
	workspaceProject := st.workspaceProject
	adapterConfig := st.adapterConfig
	agent, ok := m.agents.Agent(cfg.Harness)
	if !ok {
		m.rollbackSeedSpawnWorkspace(ctx, st.rec, ws, workspaceProject, false, false)
		return spawnFail(fmt.Errorf("spawn %s: %w: no agent adapter for harness %q", id, ErrUnknownHarness, cfg.Harness))
	}
	var env map[string]string
	var err error
	st.rec, env, err = m.prepareWorkerLaunchEnv(ctx, st.rec, st.project.Config.Env)
	if err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, st.rec, ws, workspaceProject, true, false)
		return spawnFail(wrapSpawnStage(id, ErrSpawnBrowser, err))
	}
	m.augmentAgentRuntimeEnv(agent, env)
	pinRuntimePermissionEnv(env, adapterConfig.Permissions)
	pinPromptCacheTTL(env, cfg.Harness, cfg.Kind, st.project.Config)
	if validator, ok := agent.(ports.AgentLaunchAuthValidator); ok {
		status, authErr := validator.ValidateLaunchAuth(ctx, ws.Path, env)
		if authErr != nil {
			m.logger.Debug("spawn: launch authentication probe inconclusive; continuing",
				"sessionID", id, "harness", cfg.Harness, "error", agentlaunch.RedactValues(authErr.Error(), st.project.Config.Env))
		} else if status == ports.AgentAuthStatusUnauthorized {
			m.rollbackSeedSpawnWorkspace(ctx, st.rec, ws, workspaceProject, true, false)
			return spawnFail(fmt.Errorf("spawn %s: %w", id, ports.ErrAgentAuthRequired))
		}
	}
	if err := m.prepareWorkspace(ctx, agent, id, ws.Path, st.systemPrompt, st.systemPromptFile, adapterConfig, env); err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, st.rec, ws, workspaceProject, false, false)
		return spawnFail(wrapSpawnStage(id, ErrSpawnPrepare, err))
	}
	launchCfg := ports.LaunchConfig{
		Policy:           st.policy,
		DataDir:          m.dataDir,
		SessionID:        string(id),
		WorkspacePath:    ws.Path,
		Kind:             cfg.Kind,
		Prompt:           st.prompt,
		SystemPrompt:     st.systemPrompt,
		SystemPromptFile: st.systemPromptFile,
		IssueID:          string(cfg.IssueID),
		Config:           adapterConfig,
		Permissions:      adapterConfig.Permissions,
		// The fence (protectState) is built from the launch env: without the run
		// file in it a fresh Claude launch runs unsandboxed (B454).
		Env: env,
	}
	delivery, err := agent.GetPromptDeliveryStrategy(ctx, launchCfg)
	if err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, st.rec, ws, workspaceProject, true, false)
		return spawnFail(wrapSpawnStage(id, ErrSpawnPromptDelivery, err))
	}
	afterStartPrompt := st.prompt
	if delivery == ports.PromptDeliveryAfterStart {
		afterStartPrompt, err = buildAfterStartPrompt(ctx, agent, launchCfg)
		if err != nil {
			m.rollbackSeedSpawnWorkspace(ctx, st.rec, ws, workspaceProject, true, false)
			return spawnFail(wrapSpawnStage(id, ErrSpawnPromptDelivery, err))
		}
		launchCfg.Prompt = ""
	}
	argv, err := agent.GetLaunchCommand(ctx, launchCfg)
	if err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, st.rec, ws, workspaceProject, true, false)
		return spawnFail(wrapSpawnStage(id, ErrSpawnLaunchCommand, err))
	}
	// Pre-flight: confirm argv[0] actually exists on PATH (or as an absolute
	// path the adapter returned) BEFORE handing the launch to the runtime.
	// tmux happily creates a session+pane around a missing command, so an
	// unresolved binary would leak through as a "live" session that never ran.
	if err := m.validateAgentBinary(argv); err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, st.rec, ws, workspaceProject, true, false)
		return spawnFail(fmt.Errorf("spawn %s: %w", id, err))
	}
	m.augmentRuntimePATHForLaunchBinary(ctx, env, argv)
	argv, launchID, err := m.superviseAgentProcess(agent, id, env, argv)
	if err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, st.rec, ws, workspaceProject, true, false)
		return spawnFail(wrapSpawnStage(id, ErrSpawnSupervisor, err))
	}
	if err := m.prepareRuntimeLaunch(ctx, agent, id, ws.Path, st.systemPrompt, st.systemPromptFile, adapterConfig, env); err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, st.rec, ws, workspaceProject, true, false)
		return spawnFail(wrapSpawnStage(id, ErrSpawnPrepare, err))
	}
	if err := m.lcm.PrepareLaunch(id, launchID); err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, st.rec, ws, workspaceProject, true, false)
		return spawnFail(wrapSpawnStage(id, ErrSpawnPrepareLaunch, err))
	}
	st.agent = agent
	st.env = env
	st.launchCfg = launchCfg
	st.delivery = delivery
	st.afterStartPrompt = afterStartPrompt
	st.argv = argv
	st.launchID = launchID
	return nil
}

// spawnAcquireCodex takes the Codex controller admission slot. Spawn defers its
// release.
func (m *Manager) spawnAcquireCodex(ctx context.Context, st *spawnState) *spawnExit {
	release, err := m.acquireCodexControllerAdmission(ctx, st.cfg.Harness)
	if err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, st.rec, st.ws, st.workspaceProject, true, false)
		return spawnFail(fmt.Errorf("spawn %s: %w", st.id, err))
	}
	st.releaseCodex = release
	return nil
}

// spawnCreateRuntime starts the runtime for the prepared launch.
func (m *Manager) spawnCreateRuntime(ctx context.Context, st *spawnState) *spawnExit {
	id := st.id
	ws := st.ws
	handle, err := m.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     id,
		WorkspacePath: ws.Path,
		Argv:          st.argv,
		Env:           st.env,
	})
	if err != nil {
		// A create that timed out or failed midway may still have started the
		// runtime (tmux finishes new-session after the client's deadline), which
		// would be left running in a workspace we are about to delete.
		if leaked, ok := m.destroyPossibleRuntimeAfterCreateFailure(ctx, err); !ok {
			// The runtime may still be running in this workspace. Keep the
			// workspace and the session row, record the runtime handle on it, and
			// park it terminated so the reconcile reap pass can retry the destroy.
			m.keepSpawnRecordForLeakedRuntime(ctx, st.rec, ws, leaked)
			return spawnFail(wrapSpawnStage(id, ErrRuntimeCreate, agentlaunch.RedactError(err, st.project.Config.Env)))
		}
		m.rollbackSeedSpawnWorkspace(ctx, st.rec, ws, st.workspaceProject, true, false)
		return spawnFail(wrapSpawnStage(id, ErrRuntimeCreate, agentlaunch.RedactError(err, st.project.Config.Env)))
	}
	st.handle = handle
	return nil
}

// spawnMarkSpawned records the spawned session and delivers an after-start
// prompt, rolling the runtime and workspace back if either fails.
func (m *Manager) spawnMarkSpawned(ctx context.Context, st *spawnState) *spawnExit {
	cfg := &st.cfg
	id := st.id
	ws := st.ws
	handle := st.handle
	rec := st.rec
	workspaceProject := st.workspaceProject
	prompt := st.prompt
	metadata := domain.SessionMetadata{
		Permissions:               rec.Metadata.Permissions,
		Branch:                    ws.Branch,
		WorkspacePath:             ws.Path,
		WorkspaceRepoPath:         ws.RepoPath,
		RuntimeHandleID:           handle.ID,
		RuntimeLaunchID:           st.launchID,
		Prompt:                    prompt,
		LatestUserPrompt:          prompt,
		BrowserCapabilityVerifier: rec.Metadata.BrowserCapabilityVerifier,
		ArtifactDir:               rec.Metadata.ArtifactDir,
		// The user-visible resolved selection is Model for regular harnesses and
		// Mode for adapters whose catalog is a mode list (e.g. Amp). If an explicit
		// Model override exists it wins; otherwise fall back to the resolved Mode.
		Model:  resolvedModelForMetadata(cfg.Harness, st.agentConfig, st.adapterConfig),
		Effort: st.agentConfig.Effort,
	}
	if prompt != "" {
		metadata.LatestUserPromptAt = m.clock()
	}
	if st.projectKind == domain.ProjectKindSingleRepo {
		metadata.DiffBaseSHA, metadata.DiffBaseRef = resolveSpawnDiffBase(ctx, ws.Path, ws.BaseRef)
	}
	if cfg.AutomationRunID != nil {
		rec.Metadata = metadata
		rec.UpdatedAt = m.clock()
		if err := m.store.UpdateSession(ctx, rec); err != nil {
			runtimeDestroyed := m.destroySpawnRuntimeAfterFailure(ctx, handle)
			workspaceDestroyed := m.rollbackPreparedSpawnWorkspaceAfterFailure(ctx, rec, ws, workspaceProject, runtimeDestroyed)
			m.markSpawnFailedTerminatedAfterFailure(ctx, id, runtimeDestroyed && workspaceDestroyed)
			return spawnFail(wrapSpawnStage(id, ErrSpawnCommit, err))
		}
	}
	if err := m.lcm.MarkSpawned(ctx, id, metadata); err != nil {
		runtimeDestroyed := m.destroySpawnRuntimeAfterFailure(ctx, handle)
		m.rollbackPreparedSpawnWorkspaceAfterFailure(ctx, rec, ws, workspaceProject, runtimeDestroyed)
		m.markSpawnFailedTerminatedAfterFailure(ctx, id, false)
		return spawnFail(wrapSpawnStage(id, ErrSpawnCommit, err))
	}
	if st.delivery == ports.PromptDeliveryAfterStart {
		if err := m.deliverAfterStartPrompt(ctx, st.agent, st.launchCfg, handle, id, st.afterStartPrompt); err != nil {
			runtimeDestroyed := m.destroySpawnRuntimeAfterFailure(ctx, handle)
			workspaceDestroyed := m.rollbackPreparedSpawnWorkspaceAfterFailure(ctx, rec, ws, workspaceProject, runtimeDestroyed)
			m.markSpawnFailedTerminatedAfterFailure(ctx, id, runtimeDestroyed && workspaceDestroyed)
			return spawnFail(wrapSpawnStage(id, ErrSpawnDeliverPrompt, err))
		}
	}
	st.rec = rec
	return nil
}

// spawnFinish commits the automation/client-request markers, reloads the
// record, and hands task naming to the background.
func (m *Manager) spawnFinish(ctx context.Context, st *spawnState) *spawnExit {
	cfg := &st.cfg
	id := st.id
	if cfg.AutomationRunID != nil {
		if err := m.markAutomationLaunchCompleted(ctx, id); err != nil {
			return spawnFail(wrapSpawnStage(id, ErrSpawnCommit, err))
		}
	}
	if cfg.ClientRequestID != "" {
		if err := m.store.CommitClientRequestSession(ctx, id); err != nil {
			return spawnFail(wrapSpawnStage(id, ErrSpawnCommit, err))
		}
	}
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return spawnFail(err)
	}
	st.rec = rec
	st.namingHandedOff = true
	m.nameTaskBranchLater(ctx, rec, st.ws, st.workspaceProject, st.taskName, st.cancelNaming)
	return &spawnExit{rec: rec, promptBytes: st.promptBytes, systemPromptBytes: st.systemPromptBytes}
}
