package sessionmanager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// The chat-mode controller launch.
//
// It is deliberately a separate path rather than conditionals threaded through
// the terminal launch: a chat session starts no agent process inside a runtime,
// has no pane to write into, and has no prompt-delivery strategy. Sharing that
// code would mean every step asking which kind of session it is in.
//
// What the two modes DO share is everything above this point — project
// resolution, prompts, the seed row, the worktree, provisioning, attachments —
// which is why the branch sits where it does and not higher.

type chatBackgroundTaskRunner interface {
	RunBackgroundTask(context.Context, domain.AgentHarness, ports.ChatStartConfig, string) (string, error)
}

// RunBackgroundTask executes one short-lived model call with id's harness and
// the currently active authenticated account. It creates no Tallpine session or terminal.
func (m *Manager) RunBackgroundTask(
	ctx context.Context,
	id domain.SessionID,
	systemPrompt, prompt string,
) (string, error) {
	runner, ok := m.chat.(chatBackgroundTaskRunner)
	if !ok {
		return "", ports.ErrChatUnsupported
	}
	return m.runBackgroundTask(ctx, id, systemPrompt, prompt, runner.RunBackgroundTask)
}

// RunPromptSuggestionTask preserves the requested turn's native context when available.
func (m *Manager) RunPromptSuggestionTask(ctx context.Context, id domain.SessionID, turnID, systemPrompt, prompt string) (string, error) {
	runner, ok := m.chat.(interface {
		RunPromptSuggestionTask(context.Context, domain.SessionID, string, domain.AgentHarness, ports.ChatStartConfig, string) (string, error)
	})
	if !ok {
		return m.RunBackgroundTask(ctx, id, systemPrompt, prompt)
	}
	return m.runBackgroundTask(ctx, id, systemPrompt, prompt, func(ctx context.Context, harness domain.AgentHarness, cfg ports.ChatStartConfig, prompt string) (string, error) {
		return runner.RunPromptSuggestionTask(ctx, id, turnID, harness, cfg, prompt)
	})
}

func (m *Manager) runBackgroundTask(ctx context.Context, id domain.SessionID, systemPrompt, prompt string, run func(context.Context, domain.AgentHarness, ports.ChatStartConfig, string) (string, error)) (string, error) {
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return "", err
	}
	if rec.IsTerminated {
		return "", fmt.Errorf("session %s is terminated", id)
	}
	// Existing background titles and suggestions use the provider's default effort.
	rec.Metadata.Effort = ""
	return m.runRecordBackgroundTask(ctx, rec, systemPrompt, prompt, run)
}

func (m *Manager) runRecordBackgroundTask(ctx context.Context, rec domain.SessionRecord, systemPrompt, prompt string, run func(context.Context, domain.AgentHarness, ports.ChatStartConfig, string) (string, error)) (string, error) {
	releaseHarness, err := m.beginHarnessUse(rec.Harness)
	if err != nil {
		return "", err
	}
	defer releaseHarness()
	releaseCodex, err := m.acquireCodexControllerAdmission(ctx, rec.Harness)
	if err != nil {
		return "", err
	}
	defer releaseCodex()

	root := filepath.Join(m.dataDir, "background-tasks")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create background task root: %w", err)
	}
	workspace, err := os.MkdirTemp(root, "title-")
	if err != nil {
		return "", fmt.Errorf("create background task workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(workspace) }()

	config := ports.AgentConfig{
		Model: rec.Metadata.Model, Effort: rec.Metadata.Effort, Permissions: backgroundTaskPermissions(rec.Harness),
	}
	// Not the worker's scratch env: the task gets a temp dir inside its own
	// workspace, removed with it, unless the project's env names one.
	env, err := m.runtimeEnvFor(rec.ID, rec.ProjectID, rec.IssueID, nil, m.accountOverride(rec.ID), false, "")
	if err != nil {
		m.logger.Warn("session agent account env incomplete", "session", rec.ID, "error", err)
	}
	var projectEnv map[string]string
	if rec.ProjectID != "" {
		if project, err := m.loadProject(ctx, rec.ProjectID); err == nil {
			projectEnv = project.Config.Env
		}
	}
	if err := applyBackgroundTaskTempEnv(env, projectEnv, workspace, envKeysCaseInsensitive); err != nil {
		return "", fmt.Errorf("create background task temp dir: %w", err)
	}
	if m.agents != nil {
		if agent, found := m.agents.Agent(rec.Harness); found {
			m.augmentAgentRuntimeEnv(agent, env)
		}
	}
	// This provider call is not the worker session. Suppress session-scoped hooks
	// so its prompt and response cannot be projected into the worker's history.
	deleteProtectedEnv(env, EnvSessionID, envKeysCaseInsensitive)
	pinRuntimePermissionEnv(env, config.Permissions)

	return run(ctx, rec.Harness, ports.ChatStartConfig{
		Policy:        m.decideFor(ctx, rec),
		DataDir:       m.dataDir,
		WorkspacePath: workspace,
		Env:           env,
		Model:         config.Model,
		Effort:        config.Effort,
		Permissions:   config.Permissions,
		SystemPrompt:  systemPrompt,
	}, prompt)
}

func backgroundTaskPermissions(domain.AgentHarness) ports.PermissionMode {
	// Every driver gets Tallpine's workspace sandbox so title generation cannot
	// inherit a bypass policy.
	return ports.PermissionModeAcceptEdits
}
