package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// spawnLoadProject rejects unavailable artifact output, replays a repeated
// client request, and loads the project.
func (m *Manager) spawnLoadProject(ctx context.Context, st *spawnState) *spawnExit {
	cfg := &st.cfg
	if cfg.RequestedOutput == domain.RequestedOutputArtifact && m.artifactDir("") == "" {
		return spawnFail(fmt.Errorf("spawn: %w", ErrArtifactOutputUnavailable))
	}
	if cfg.ClientRequestID != "" {
		existing, found, err := m.store.GetSessionByClientRequestID(ctx, cfg.ClientRequestID)
		if err != nil {
			return spawnFail(err)
		}
		if found {
			rec, err := replayClientRequest(existing, cfg.ClientRequestHash)
			return &spawnExit{rec: rec, err: err}
		}
	}
	project, err := m.loadProject(ctx, cfg.ProjectID)
	if err != nil {
		return spawnFail(fmt.Errorf("spawn: %w", err))
	}
	st.project = project
	return nil
}

// spawnResolveHarness validates the project kind, inherits permissions, picks
// the harness and takes the installer guard.
func (m *Manager) spawnResolveHarness(ctx context.Context, st *spawnState) *spawnExit {
	cfg := &st.cfg
	project := st.project
	standalone := cfg.ProjectID == ""
	projectKind := project.Kind.WithDefault()
	if standalone {
		projectKind = domain.ProjectKindScratch
	}
	st.projectKind = projectKind
	if standalone && cfg.Kind != domain.KindWorker {
		return spawnFail(fmt.Errorf("spawn: standalone sessions must be workers"))
	}
	if projectKind == domain.ProjectKindScratch && strings.TrimSpace(cfg.Branch) != "" {
		return spawnFail(fmt.Errorf("spawn: %w", ErrScratchBranchUnsupported))
	}
	if cfg.ParentSessionID != "" && cfg.AgentConfig.Permissions == "" {
		permissions, err := m.inheritedSpawnPermissions(ctx, cfg.ProjectID, cfg.ParentSessionID)
		if err != nil {
			return spawnFail(fmt.Errorf("spawn: %w", err))
		}
		cfg.AgentConfig.Permissions = permissions
	}
	// A per-project role override picks the harness when the spawn names none,
	// so a project can default workers to one agent and orchestrators to another.
	configuredHarness := cfg.Harness == ""
	cfg.Harness = effectiveHarnessFor(cfg.Harness, cfg.Kind, cfg.Role, project.Config)
	if configuredHarness {
		// Stored project config may still name a removed harness. Only config
		// falls back; an explicit unsupported request is rejected below.
		if resolved, replaced := cfg.Harness.OrFallback(); replaced {
			m.logger.Warn("project config names an unsupported harness; falling back",
				"project", cfg.ProjectID, "role", roleConfigName(cfg.Kind), "configured", cfg.Harness, "fallback", resolved)
			cfg.Harness = resolved
		}
	}
	if cfg.Harness == "" && cfg.Kind == domain.KindOrchestrator {
		// A freshly added project has no orchestrator agent configured; use the
		// daemon default rather than failing (orchestrator new has no --harness).
		cfg.Harness = launchDefaultHarness()
	}
	if cfg.Harness == "" {
		return spawnFail(fmt.Errorf("spawn: %w: configure project %s.agent or pass --harness", ErrMissingHarness, roleConfigName(cfg.Kind)))
	}
	releaseHarness, err := m.beginHarnessUse(cfg.Harness)
	if err != nil {
		return spawnFail(fmt.Errorf("spawn: %w", err))
	}
	st.releaseHarness = releaseHarness
	// Reject an unknown harness before any durable state is created. Doing this
	// after CreateSession would leave a terminated orphan row and waste a
	// worktree on a spawn that can never launch.
	if _, ok := m.agents.Agent(cfg.Harness); !ok {
		return spawnFail(fmt.Errorf("spawn: %w: %q", ErrUnknownHarness, cfg.Harness))
	}
	return nil
}

// spawnResolveModelAndMode resolves the effective agent config, validates the
// model, and picks the controller mode, all before anything durable exists.
func (m *Manager) spawnResolveModelAndMode(ctx context.Context, st *spawnState) *spawnExit {
	cfg := &st.cfg
	project := st.project
	// Resolve the effective agent config (project base + role override + spawn
	// override) and validate the model before any durable state is created. A
	// model the harness cannot honor should not leave a seed row behind.
	st.policy = m.decide(ctx, cfg.ProjectID, cfg.Kind, cfg.Harness, "")
	st.requestedPermissions = applySpawnAgentConfig(effectiveAgentConfigFor(cfg.Harness, cfg.Kind, cfg.Role, project.Config), cfg.AgentConfig).Permissions
	agentConfig := applySpawnAgentConfigUnder(st.policy, cfg.Harness, effectiveAgentConfigFor(cfg.Harness, cfg.Kind, cfg.Role, project.Config), cfg.AgentConfig)
	policyModel, err := project.Config.ResolveModel(cfg.Harness, agentConfig.Model)
	if err != nil {
		return spawnFail(fmt.Errorf("spawn: %w: %s", ErrUnsupportedModel, err.Error()))
	}
	agentConfig.Model = policyModel
	if err := validateSpawnModel(cfg.Harness, agentConfig.Model); err != nil {
		return spawnFail(fmt.Errorf("spawn: %w: %s", ErrUnsupportedModel, err.Error()))
	}
	// Resolve the controller mode here, before anything durable is created, for
	// the same reason an unknown harness is rejected above: an explicit Chat
	// request Tallpine cannot honor should cost nothing, not leave a terminated row and
	// a worktree behind. Chat inherited from the daemon preference is best-effort:
	// if it is unavailable for this harness or installation, fall back to TUI.
	modeExplicitlyRequested := cfg.RequestedMode.Valid()
	mode := m.resolveSessionMode(ctx, cfg.Kind, cfg.RequestedMode, project.Config.DefaultSessionMode)
	if mode == domain.SessionModeChat {
		if m.chat == nil {
			if modeExplicitlyRequested {
				return spawnFail(fmt.Errorf("spawn: %w: chat mode is not available in this build", ports.ErrChatUnsupported))
			}
			m.logger.Warn("spawn: default Chat unavailable; falling back to TUI",
				"harness", cfg.Harness, "error", ports.ErrChatUnsupported)
			mode = domain.SessionModeTUI
		} else if err := m.chat.PreflightChat(ctx, cfg.Harness, agentConfig.Permissions, st.policy); err != nil {
			fallbackAllowed := errors.Is(err, ports.ErrChatUnsupported) ||
				errors.Is(err, ports.ErrChatDriverUnavailable) ||
				errors.Is(err, ports.ErrChatDriverIncompatible) ||
				errors.Is(err, ports.ErrChatAuthRequired)
			if modeExplicitlyRequested || !fallbackAllowed ||
				errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return spawnFail(fmt.Errorf("spawn: %w", err))
			}
			m.logger.Warn("spawn: default Chat unavailable; falling back to TUI",
				"harness", cfg.Harness, "error", err)
			mode = domain.SessionModeTUI
		}
		if mode == domain.SessionModeChat {
			resolved, err := m.resolveChatAgentConfig(ctx, st.policy, *cfg, project.Config)
			switch {
			case err != nil && !modeExplicitlyRequested && cfg.Harness == domain.HarnessClaudeCode &&
				(errors.Is(err, ErrUnsupportedModel) || errors.Is(err, ports.ErrModelCapabilitiesUnavailable)) && domain.IsClaudeModelID(agentConfig.Model):
				// Chat cannot confirm a model the account list lacks (it would
				// keep Claude Code's own model); a terminal session can run it.
				m.logger.Warn("spawn: default Chat cannot confirm an unlisted model; falling back to TUI",
					"harness", cfg.Harness, "model", agentConfig.Model, "error", err)
				mode = domain.SessionModeTUI
			case err != nil:
				return spawnFail(fmt.Errorf("spawn: %w", err))
			default:
				cfg.AgentConfig = resolved
				cfg.AgentConfigResolved = true
			}
		}
	}
	if mode == domain.SessionModeTUI && (cfg.Harness == domain.HarnessClaudeCode || cfg.Harness == domain.HarnessCodex || strings.TrimSpace(cfg.AgentConfig.Effort) != "") {
		resolved, err := m.resolveTUIAgentConfig(ctx, st.policy, *cfg, project.Config, agentConfig)
		if err != nil {
			return spawnFail(fmt.Errorf("spawn: %w", err))
		}
		cfg.AgentConfig = resolved
		cfg.AgentConfigResolved = true
		agentConfig = resolved
	}
	if mode == domain.SessionModeTUI {
		if err := m.admitTerminalFence(st.policy, cfg.Harness, agentConfig.Permissions); err != nil {
			return spawnFail(fmt.Errorf("spawn: %w", err))
		}
	}
	cfg.RequestedMode = mode

	// Adapters whose model picker is an agent-owned mode list (e.g. Amp) keep
	// their selectable values in AgentConfig.Mode. Normalize a copy for the
	// adapter so `tallpine spawn --agent amp --model high` launches Amp with `--mode
	// high`, while metadata keeps the original resolved Model for the API view.
	adapterConfig := normalizeAgentConfigForHarness(cfg.Harness, agentConfig)

	// A chat session runs no agent inside a terminal runtime, so the terminal
	// prerequisites are not its concern.
	if mode == domain.SessionModeTUI {
		if err := m.validateRuntimePrerequisites(); err != nil {
			return spawnFail(fmt.Errorf("spawn: %w", err))
		}
	}
	if mode == domain.SessionModeTUI {
		// A fresh terminal launch applies the Settings compaction default and
		// the model window like a relaunch does.
		adapterConfig.Launch = m.freshLaunchOptions(ctx, cfg.Harness, cfg.Kind, cfg.ProjectID, project.Config, adapterConfig.Model, adapterConfig.Launch)
	}
	st.agentConfig = agentConfig
	st.adapterConfig = adapterConfig
	st.mode = mode
	return nil
}
