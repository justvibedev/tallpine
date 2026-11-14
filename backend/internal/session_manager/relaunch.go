package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (m *Manager) relaunchSession(ctx context.Context, operation string, rec domain.SessionRecord, project domain.ProjectRecord, ws ports.WorkspaceInfo, restartHandle *ports.RuntimeHandle) (RestoreResult, error) {
	return m.relaunchSessionWithPolicy(
		ctx,
		operation,
		rec,
		project,
		ws,
		restartHandle,
		false,
		false,
		domain.SessionInterfaceTransitionHistoryStrict,
	)
}

func (m *Manager) relaunchSessionWithPolicy(
	ctx context.Context,
	operation string,
	rec domain.SessionRecord,
	project domain.ProjectRecord,
	ws ports.WorkspaceInfo,
	restartHandle *ports.RuntimeHandle,
	forceFresh, requireNativeHistory bool,
	historyPolicy domain.SessionInterfaceTransitionHistoryPolicy,
) (RestoreResult, error) {
	generation := ""
	if !forceFresh {
		var err error
		generation, err = m.turnFallbackHandoffGeneration(ctx, rec, "")
		if err != nil {
			return RestoreResult{}, err
		}
	}
	return m.relaunchSessionWithPolicyAndGeneration(ctx, operation, rec, project, ws, restartHandle, forceFresh, requireNativeHistory, generation, historyPolicy, nil)
}

func (m *Manager) relaunchSessionWithPolicyAndGeneration(ctx context.Context, operation string, rec domain.SessionRecord, project domain.ProjectRecord, ws ports.WorkspaceInfo, restartHandle *ports.RuntimeHandle, forceFresh, requireNativeHistory bool, reservedGeneration string, historyPolicy domain.SessionInterfaceTransitionHistoryPolicy, promptOverride *string) (RestoreResult, error) {
	// Kill drops an empty artifact dir; the restored agent is told to write there.
	m.reserveArtifactDir(rec.ID)
	// Relaunch dispatches from the currently committed persisted mode, never from
	// a caller hint. The interface-transition coordinator changes that fact only
	// after stopping the old controller, then reuses this ordinary restore path.
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if forceFresh {
			rec.Metadata.ProviderConversationID = ""
		} else if strings.TrimSpace(rec.Metadata.ProviderConversationID) == "" {
			return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, ErrIncompleteHandle)
		}
		return m.resumeChatController(
			ctx, operation, rec, project, ws, requireNativeHistory, false, reservedGeneration, historyPolicy,
		)
	}

	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return RestoreResult{}, fmt.Errorf("%s %s: no agent adapter for harness %q", operation, rec.ID, rec.Harness)
	}
	systemPrompt, systemPromptFile, err := m.restorePromptFiles(ctx, operation, rec, forceFresh)
	if err != nil {
		return RestoreResult{}, err
	}

	decision := m.decideFor(ctx, rec)
	capped := restoredAgentConfigUnder(decision, rec, project.Config)
	if err := m.admitTerminalFence(decision, rec.Harness, capped.Permissions); err != nil {
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, err)
	}
	agentConfig, _, err := m.enforceResumeModel(ctx, rec, capped)
	if err != nil {
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, err)
	}
	m.recordPolicy(ctx, rec.ID, decision, restoredAgentConfig(rec, project.Config).Permissions, agentConfig.Permissions, launchFacts(decision, project.Config.Env), m.fenceGapFacts(decision, rec.Harness, agentConfig.Permissions))
	agentConfig.Launch = m.launchOptionsWithWindow(ctx, rec, project.Config, agentConfig.Model)
	// Only a provider switch passes its own prompt; it rolls back a failed start itself.
	switching := promptOverride != nil
	// A switch target that has not started yet: its note goes in place of the task.
	savedNote := false
	if note := agentConfig.Launch.HandoffNote; note != "" && !switching && rec.Metadata.AgentSessionID == "" {
		promptOverride, savedNote = &note, true
	}
	agentConfig.Launch.HandoffNote = ""
	var env map[string]string
	rec, env, err = m.prepareWorkerLaunchEnv(ctx, rec, project.Config.Env)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("%s %s: browser capability: %w", operation, rec.ID, err)
	}
	m.augmentAgentRuntimeEnv(agent, env)
	pinRuntimePermissionEnv(env, agentConfig.Permissions)
	if err := m.checkRestoreLaunchAuth(ctx, agent, rec, ws.Path, env); err != nil {
		return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, err)
	}
	if err := m.prepareWorkspace(ctx, agent, rec.ID, ws.Path, systemPrompt, systemPromptFile, agentConfig, env); err != nil {
		return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, err)
	}
	if requireNativeHistory && !forceFresh {
		if err := verifyNativeHistoryExists(ctx, agent, rec, ws.Path, env); err != nil {
			return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, err)
		}
	}
	// A handoff note is for this launch only; the session keeps the user's prompt.
	savedPrompt := rec.Metadata.Prompt
	if promptOverride != nil {
		rec.Metadata.Prompt = *promptOverride
	}
	plan, err := m.planRestoreLaunch(ctx, decision, agent, rec, ws.Path, systemPrompt, systemPromptFile, agentConfig, env, forceFresh, requireNativeHistory, promptOverride != nil)
	argv, initialization, delivery, mode, freshNativeSessionID := plan.argv, plan.initialization, plan.delivery, plan.mode, plan.freshNativeSessionID
	if err != nil {
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, err)
	}
	if requireNativeHistory && !forceFresh && mode != RestoreModeNative {
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, ErrNotResumable)
	}
	launchCfg := restoreLaunchConfig(decision, m.dataDir, rec, ws.Path, systemPrompt, systemPromptFile, agentConfig, freshNativeSessionID)
	afterStartPrompt := rec.Metadata.Prompt
	if delivery == ports.PromptDeliveryAfterStart && mode != RestoreModeNative {
		afterStartPrompt, err = buildAfterStartPrompt(ctx, agent, launchCfg)
		if err != nil {
			m.cleanupSystemPromptDir(rec.ID)
			return RestoreResult{}, fmt.Errorf("%s %s: after-start prompt: %w", operation, rec.ID, err)
		}
	}
	if err := m.validateAgentBinary(argv); err != nil {
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, err)
	}
	m.augmentRuntimePATHForLaunchBinary(ctx, env, argv)
	launchID := strings.TrimSpace(reservedGeneration)
	if launchID == "" {
		argv, launchID, err = m.superviseAgentProcess(agent, rec.ID, env, argv)
	} else {
		argv, err = m.wrapAgentProcessWithLaunchID(agent, rec.ID, env, argv, launchID, true)
	}
	if err != nil {
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: supervisor: %w", operation, rec.ID, err)
	}
	if err := m.prepareRuntimeLaunch(ctx, agent, rec.ID, ws.Path, systemPrompt, systemPromptFile, agentConfig, env); err != nil {
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: runtime preparation: %w", operation, rec.ID, err)
	}
	if err := m.lcm.PrepareLaunch(rec.ID, launchID); err != nil {
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: prepare launch: %w", operation, rec.ID, err)
	}
	defer m.lcm.CancelLaunch(rec.ID, launchID)
	releaseCodexAdmission, err := m.acquireCodexControllerAdmission(ctx, rec.Harness)
	if err != nil {
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, err)
	}
	defer releaseCodexAdmission()
	runtimeCfg := ports.RuntimeConfig{
		SessionID:     rec.ID,
		WorkspacePath: ws.Path,
		Argv:          argv,
		Env:           env,
	}
	handle, err := m.createOrRestartRuntime(ctx, runtimeCfg, restartHandle)
	if err != nil {
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: runtime: %w", operation, rec.ID, agentlaunch.RedactError(err, project.Config.Env))
	}
	// A runtime accepting Restart only proves that its supervisor was created.
	// Keep the previous launch (and stale/release references) until the exact
	// resumed child is observable. A failed native resume remains recoverable.
	if requireNativeHistory && !forceFresh {
		if startErr := m.verifyResumedLaunch(ctx, rec, handle, launchID); startErr != nil {
			return RestoreResult{}, fmt.Errorf("%s %s: resumed agent did not start: %w", operation, rec.ID, startErr)
		}
	}
	metadata := m.restoredSessionMetadata(rec, ws, handle, launchID, savedPrompt, mode, initialization != nil, requireNativeHistory && !forceFresh)
	if err := m.lcm.MarkSpawned(ctx, rec.ID, metadata); err != nil {
		m.destroySpawnRuntimeAfterFailure(ctx, handle)
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: completed: %w", operation, rec.ID, err)
	}
	if err := m.finishRestoreLaunch(ctx, operation, agent, rec, handle, launchID, launchCfg, plan.initialization, delivery, mode, afterStartPrompt, switching, savedNote); err != nil {
		return RestoreResult{}, err
	}
	updated, err := m.getRecord(ctx, rec.ID)
	if err != nil {
		return RestoreResult{}, err
	}
	return RestoreResult{Session: updated, Mode: mode}, nil
}

func (m *Manager) restartRuntime(ctx context.Context, handle ports.RuntimeHandle, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	alive, err := m.runtime.IsAlive(ctx, handle)
	if err != nil {
		if !errors.Is(err, ports.ErrRuntimeUnavailable) {
			return ports.RuntimeHandle{}, fmt.Errorf("probe existing runtime: %w", err)
		}
		// The runtime infrastructure itself is gone (e.g. the tmux server was
		// killed). Restore/restart is exactly the recovery path for that
		// outage, so proceed as "no existing runtime" and create a fresh one.
		alive = false
	}
	if alive {
		if restarter, ok := m.runtime.(ports.RuntimeRestarter); ok {
			return restarter.Restart(ctx, handle, cfg)
		}
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			return ports.RuntimeHandle{}, fmt.Errorf("destroy existing runtime: %w", err)
		}
	}
	return m.runtime.Create(ctx, cfg)
}

// restorePromptFiles builds the system prompt and writes its file for a relaunch.
func (m *Manager) restorePromptFiles(ctx context.Context, operation string, rec domain.SessionRecord, forceFresh bool) (systemPrompt, systemPromptFile string, err error) {
	systemPrompt, err = m.buildSystemPromptFor(ctx, rec.Kind, rec.ProjectID, rec.ID, rec.RequestedOutput, rec.Role, false)
	if err != nil {
		return "", "", fmt.Errorf("%s %s: system prompt: %w", operation, rec.ID, err)
	}
	if !forceFresh {
		systemPrompt = m.withLegacySwitchHandoff(ctx, rec, systemPrompt)
	}
	systemPromptFile, err = m.prepareSystemPromptFile(rec.ID, rec.Harness, systemPrompt)
	if err != nil {
		m.cleanupSystemPromptDir(rec.ID)
		return "", "", fmt.Errorf("%s %s: system prompt file: %w", operation, rec.ID, err)
	}
	return systemPrompt, systemPromptFile, nil
}

// restoredSessionMetadata is the metadata recorded for a relaunched session.
func (m *Manager) restoredSessionMetadata(rec domain.SessionRecord, ws ports.WorkspaceInfo, handle ports.RuntimeHandle, launchID, savedPrompt string, mode RestoreMode, hasInitialization, requireNativeResume bool) domain.SessionMetadata {
	metadata := domain.SessionMetadata{
		Permissions:               rec.Metadata.Permissions,
		Branch:                    ws.Branch,
		WorkspacePath:             ws.Path,
		WorkspaceRepoPath:         ws.RepoPath,
		RuntimeHandleID:           handle.ID,
		RuntimeLaunchID:           launchID,
		AgentSessionID:            rec.Metadata.AgentSessionID,
		Prompt:                    savedPrompt,
		BrowserCapabilityVerifier: rec.Metadata.BrowserCapabilityVerifier,
	}
	// Bind an exact native resume to the target launch immediately. Passive Codex
	// resumes do not necessarily emit SessionStart until the next user turn, and
	// Claude emits SessionStart after its resume process is already running, but
	// both adapters' explicit resume commands name the exact stored conversation.
	// The interface coordinator provides the same guarantee after it freezes Chat
	// and transfers the required native history. Fresh and fallback launches
	// still require current-generation identity proof from their hooks.
	bindNativeIdentity := mode == RestoreModeNative &&
		(rec.Harness == domain.HarnessCodex || rec.Harness == domain.HarnessClaudeCode || m.terminalVersionRefreshSupported(rec.Harness))
	if !hasInitialization && (bindNativeIdentity || requireNativeResume) && strings.TrimSpace(metadata.AgentSessionID) != "" {
		metadata.AgentSessionIDLaunchID = launchID
	}
	return metadata
}

// finishRestoreLaunch initializes a native restore, delivers the after-start
// prompt and clears a consumed handoff note.
func (m *Manager) finishRestoreLaunch(ctx context.Context, operation string, agent ports.Agent, rec domain.SessionRecord, handle ports.RuntimeHandle, launchID string, launchCfg ports.LaunchConfig, initialization ports.NativeRestoreInitialization, delivery ports.PromptDeliveryStrategy, mode RestoreMode, afterStartPrompt string, switching, savedNote bool) error {
	if initialization != nil {
		if err := m.initializeNativeRestore(ctx, rec, handle, launchID, initialization); err != nil {
			cleanupCtx, cancel := spawnRollbackContext(ctx)
			cleanupErr := m.rollbackNativeInitialization(cleanupCtx, rec, handle, launchID)
			cancel()
			return fmt.Errorf("%s %s: native initialization: %w", operation, rec.ID, errors.Join(err, cleanupErr))
		}
	}
	if delivery == ports.PromptDeliveryAfterStart || initialization != nil {
		if mode == RestoreModeNative {
			afterStartPrompt = "" // restore native state without replaying a user turn
		}
		if err := m.deliverAfterStartPrompt(ctx, agent, launchCfg, handle, rec.ID, afterStartPrompt); err != nil {
			m.destroySpawnRuntimeAfterFailure(ctx, handle)
			// A provider switch brings the source back itself, so its session
			// must not be ended here.
			if !switching {
				cleanupCtx, cancel := spawnRollbackContext(ctx)
				_ = m.lcm.MarkTerminated(cleanupCtx, rec.ID)
				cancel()
			}
			m.cleanupSystemPromptDir(rec.ID)
			return fmt.Errorf("%s %s: deliver prompt: %w", operation, rec.ID, err)
		}
	}
	if savedNote {
		m.clearHandoffNote(ctx, rec.ID)
	}
	return nil
}

// createOrRestartRuntime creates the runtime, or restarts the previous handle.
func (m *Manager) createOrRestartRuntime(ctx context.Context, runtimeCfg ports.RuntimeConfig, restartHandle *ports.RuntimeHandle) (ports.RuntimeHandle, error) {
	if restartHandle == nil {
		return m.runtime.Create(ctx, runtimeCfg)
	}
	return m.restartRuntime(ctx, *restartHandle, runtimeCfg)
}

// restoreLaunchConfig is the launch config handed to the agent adapter on relaunch.
func restoreLaunchConfig(decision agentpolicy.Decision, dataDir string, rec domain.SessionRecord, workspacePath, systemPrompt, systemPromptFile string, agentConfig ports.AgentConfig, nativeSessionID string) ports.LaunchConfig {
	return ports.LaunchConfig{
		Policy:           decision,
		DataDir:          dataDir,
		SessionID:        string(rec.ID),
		WorkspacePath:    workspacePath,
		Kind:             rec.Kind,
		Prompt:           rec.Metadata.Prompt,
		SystemPrompt:     systemPrompt,
		SystemPromptFile: systemPromptFile,
		Config:           agentConfig,
		Permissions:      agentConfig.Permissions,
		NativeSessionID:  nativeSessionID,
	}
}
