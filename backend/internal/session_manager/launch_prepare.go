package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/termtheme"
)

// preLauncher is an optional Agent capability: a step the manager runs before
// launch. Claude Code implements it to record workspace trust in ~/.claude.json
// so its interactive "do you trust this folder?" dialog can't block the headless
// pane. Adapters that don't need it simply omit the method.
type preLauncher interface {
	PreLaunch(ctx context.Context, cfg ports.LaunchConfig) error
}

// runtimeLaunchPreparer is an optional Agent capability for configuration that
// must include Tallpine's freshly assigned runtime generation. It runs after
// superviseAgentProcess has pinned TALLPINE_RUNTIME_LAUNCH_ID and before the runtime
// starts, so provider callbacks can carry the same stale-process fence used by
// native Tallpine hooks.
type runtimeLaunchPreparer interface {
	PrepareRuntimeLaunch(ctx context.Context, cfg ports.WorkspaceHookConfig) error
}

// workspaceCleaner is an optional Agent capability for durable agent-side state
// that should be released only after Tallpine has actually removed the workspace.
type workspaceCleaner interface {
	CleanupWorkspace(ctx context.Context, cfg ports.WorkspaceHookConfig) error
}

func (m *Manager) augmentAgentRuntimeEnv(agent ports.Agent, env map[string]string) {
	if augmenter, ok := agent.(interface {
		AugmentRuntimeEnv(map[string]string, string)
	}); ok {
		augmenter.AugmentRuntimeEnv(env, m.dataDir)
	}
	// Every agent, not only Cursor: CLIs that follow the terminal theme (Claude
	// Code's "auto", Codex) otherwise fall back to dark when their OSC 11 probe
	// is not answered in time across the mux, and draw near-white text on Tallpine's
	// light canvas. A value the project env already set wins, in any key case
	// where the OS folds env keys.
	termtheme.ApplyFoldingKeys(env, m.dataDir, envKeysCaseInsensitive)
}

// prepareWorkspace runs the per-session pre-launch steps before the runtime
// starts the agent: installing the workspace-local activity hooks (so early
// startup hooks can update the already-created session row), then any optional
// PreLaunch step. Shared by Spawn and Restore.
func (m *Manager) prepareWorkspace(ctx context.Context, agent ports.Agent, id domain.SessionID, workspacePath, systemPrompt, systemPromptFile string, agentConfig ports.AgentConfig, env map[string]string) error {
	if err := agent.GetAgentHooks(ctx, ports.WorkspaceHookConfig{
		SessionID:        string(id),
		WorkspacePath:    workspacePath,
		DataDir:          m.dataDir,
		Env:              env,
		SystemPrompt:     systemPrompt,
		SystemPromptFile: systemPromptFile,
		Config:           agentConfig,
	}); err != nil {
		m.cleanupPreparedAgentWorkspace(ctx, agent, id, workspacePath, env)
		return fmt.Errorf("install hooks: %w", err)
	}
	if pl, ok := agent.(preLauncher); ok {
		if err := pl.PreLaunch(ctx, ports.LaunchConfig{
			DataDir:       m.dataDir,
			SessionID:     string(id),
			WorkspacePath: workspacePath,
			Config:        agentConfig,
			Permissions:   agentConfig.Permissions,
			Env:           env,
		}); err != nil {
			m.cleanupPreparedAgentWorkspace(ctx, agent, id, workspacePath, env)
			return fmt.Errorf("pre-launch: %w", err)
		}
	}
	return nil
}

func (m *Manager) prepareRuntimeLaunch(ctx context.Context, agent ports.Agent, id domain.SessionID, workspacePath, systemPrompt, systemPromptFile string, agentConfig ports.AgentConfig, env map[string]string) error {
	preparer, ok := agent.(runtimeLaunchPreparer)
	if !ok {
		return nil
	}
	return preparer.PrepareRuntimeLaunch(ctx, ports.WorkspaceHookConfig{
		SessionID:        string(id),
		WorkspacePath:    workspacePath,
		DataDir:          m.dataDir,
		Env:              env,
		SystemPrompt:     systemPrompt,
		SystemPromptFile: systemPromptFile,
		Config:           agentConfig,
	})
}

func (m *Manager) cleanupPreparedAgentWorkspace(ctx context.Context, agent ports.Agent, id domain.SessionID, workspacePath string, env map[string]string) {
	if err := m.cleanupPreparedAgentWorkspaceStrict(ctx, agent, id, workspacePath, env); err != nil {
		m.logger.Warn("session prepare rollback: failed to clean agent workspace state",
			"session", id, "workspacePath", workspacePath, "error", err)
	}
}

func (m *Manager) cleanupPreparedAgentWorkspaceStrict(ctx context.Context, agent ports.Agent, id domain.SessionID, workspacePath string, env map[string]string) error {
	cleaner, ok := agent.(workspaceCleaner)
	if !ok {
		return nil
	}
	if err := cleaner.CleanupWorkspace(ctx, ports.WorkspaceHookConfig{
		SessionID:     string(id),
		WorkspacePath: workspacePath,
		DataDir:       m.dataDir,
		Env:           env,
	}); err != nil {
		return err
	}
	return nil
}

func (m *Manager) cleanupAgentWorkspace(ctx context.Context, rec domain.SessionRecord, workspacePath string) {
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return
	}
	cleaner, cleansWorkspace := agent.(workspaceCleaner)
	if !cleansWorkspace {
		return
	}
	env := spawnEnv(rec.ID, rec.ProjectID, rec.IssueID, m.dataDir, nil)
	if project, err := m.loadProject(ctx, rec.ProjectID); err == nil {
		env = m.teardownRuntimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env)
	} else {
		m.logger.Warn("workspace cleanup: project env unavailable; agent cleanup using Tallpine env only",
			"sessionID", rec.ID, "projectID", rec.ProjectID, "error", err)
	}
	if strings.TrimSpace(workspacePath) != "" {
		if err := cleaner.CleanupWorkspace(ctx, ports.WorkspaceHookConfig{
			DataDir:       m.dataDir,
			Env:           env,
			SessionID:     string(rec.ID),
			WorkspacePath: workspacePath,
		}); err != nil {
			m.logger.Warn("workspace cleanup: agent cleanup failed", "sessionID", rec.ID, "workspacePath", workspacePath, "error", err)
		}
	}
}

func (m *Manager) superviseAgentProcess(agent ports.Agent, id domain.SessionID, env map[string]string, argv []string) ([]string, string, error) {
	// Refresh-capable providers need the exact-generation supervisor's
	// foreground-process proof for command protection and startup checks.
	// An exit races into the non-interpreting tmux sink rather than a shell.
	refreshCapability, refreshCapable := agent.(ports.AgentTerminalVersionRefresher)
	refreshCapable = refreshCapable && refreshCapability.SupportsTerminalVersionRefresh()
	return m.superviseAgentProcessMode(agent, id, env, argv, refreshCapable)
}

func (m *Manager) superviseAgentProcessMode(agent ports.Agent, id domain.SessionID, env map[string]string, argv []string, force bool) ([]string, string, error) {
	launchID := m.newLaunchID()
	if strings.TrimSpace(launchID) == "" {
		return nil, "", errors.New("generated empty launch id")
	}
	wrapped, err := m.wrapAgentProcessWithLaunchID(agent, id, env, argv, launchID, force)
	if err != nil {
		return nil, "", err
	}
	return wrapped, launchID, nil
}

// wrapAgentProcessWithLaunchID rebuilds a preflighted launch command without
// changing its already-reserved generation.
func (m *Manager) wrapAgentProcessWithLaunchID(agent ports.Agent, id domain.SessionID, env map[string]string, argv []string, launchID string, force bool) ([]string, error) {
	if strings.TrimSpace(launchID) == "" {
		return nil, errors.New("empty launch id")
	}
	// Every provider generation is fenced, including providers that report
	// process exit through native hooks and therefore do not need the wrapper.
	// Without this env value an old source hook can overwrite the target's
	// native session id after an in-place switch.
	env[EnvRuntimeLaunchID] = launchID
	if augmenter, ok := agent.(ports.AgentRuntimeLaunchEnv); ok {
		augmenter.AugmentRuntimeLaunchEnv(env, m.dataDir, id, launchID)
	}
	detector, ok := agent.(ports.AgentExitDetector)
	if !force && (!ok || detector.ExitDetectionMode() != ports.AgentExitDetectionSupervisor) {
		return argv, nil
	}
	env[EnvSupervisedProcess] = "1"
	executable, err := m.executable()
	if err != nil {
		return nil, fmt.Errorf("resolve Tallpine executable: %w", err)
	}
	wrapped := make([]string, 0, 8+len(argv))
	wrapped = append(wrapped, executable, "agent-process", "supervise", "--session", string(id), "--launch", launchID, "--")
	wrapped = append(wrapped, argv...)
	return wrapped, nil
}
