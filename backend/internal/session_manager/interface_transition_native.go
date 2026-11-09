package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// handoffNativeConversationID is the readiness check shared by status and
// start. A reserved native id alone is not enough for a handoff: Claude Code
// only writes its transcript after the first prompt, so the id must also have
// durable provider history behind it (or positive fresh-start proof).
func (m *Manager) handoffNativeConversationID(
	ctx context.Context,
	rec domain.SessionRecord,
) (string, error) {
	id, handoff, err := m.nativeConversationID(ctx, rec)
	if err != nil {
		return "", err
	}
	return m.persistedNativeConversationID(ctx, rec, id, handoff)
}

// nativeConversationID resolves the adapter's native conversation id for the
// session's current interface. A missing id requires positive untouched-terminal
// or durable empty-Chat proof. Chat may also reserve a ProviderConversationID
// before persisting history; persistedNativeConversationID checks whether that
// id can resume or qualifies for a fresh launch. Conversation activity without
// a resumable identity must fail closed instead of discarding existing work.
func (m *Manager) nativeConversationID(
	ctx context.Context,
	rec domain.SessionRecord,
) (string, ports.AgentInterfaceHandoff, error) {
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return "", nil, fmt.Errorf("%w: no adapter for %s", ErrInterfaceHandoffUnsupported, rec.Harness)
	}
	handoff, ok := agent.(ports.AgentInterfaceHandoff)
	if !ok {
		return "", nil, fmt.Errorf("%w: %s has not declared compatible TUI and Chat identities",
			ErrInterfaceHandoffUnsupported, rec.Harness)
	}
	mode := domain.NormalizeSessionMode(rec.Mode)
	ref := ports.SessionRef{
		ID: string(rec.ID), WorkspacePath: rec.Metadata.WorkspacePath,
		Metadata: map[string]string{ports.MetadataKeyAgentSessionID: rec.Metadata.AgentSessionID},
	}
	id, ok, err := handoff.NativeConversationID(ctx, ref, mode, rec.Metadata.ProviderConversationID)
	if err != nil {
		return "", handoff, err
	}
	id = strings.TrimSpace(id)
	if !ok || id == "" {
		if m.nativeConversationNotStarted(ctx, rec, agent) {
			return "", handoff, nil
		}
		return "", handoff, fmt.Errorf("%w for %s", ErrNativeConversationMissing, rec.Harness)
	}
	// AgentSessionID is also the resume hint used to launch a TUI, so it can
	// legitimately outlive the runtime generation that originally reported it.
	// That makes it insufficient as handoff proof by itself: a failed resume may
	// leave a fresh visible TUI beside the old durable id. Only a native-id hook
	// accepted under this exact launch generation proves the two still match.
	if mode == domain.SessionModeTUI {
		launchID := strings.TrimSpace(rec.Metadata.RuntimeLaunchID)
		identityLaunchID := strings.TrimSpace(rec.Metadata.AgentSessionIDLaunchID)
		if launchID == "" || identityLaunchID != launchID {
			return "", handoff, fmt.Errorf("%w for %s", ErrNativeConversationUnverified, rec.Harness)
		}
	}
	return id, handoff, nil
}

// nativeConversationNotStarted is the only evidence that may turn a missing
// provider history into an intentional fresh handoff. A missing file or a
// reserved native id is not enough: both are also observable when persistence
// is lagging or broken after real work. Chat requires durable emptiness — an
// empty root conversation, or no conversation at all; TUI requires an
// untouched initial composer. Tallpine must have no hook-derived conversation facts
// that contradict either proof.
func (m *Manager) nativeConversationNotStarted(
	ctx context.Context,
	rec domain.SessionRecord,
	agent ports.Agent,
) bool {
	// A hook can identify a real turn without carrying its text. Neither a
	// missing transcript nor an apparently untouched composer can override that
	// durable evidence and authorize discarding the conversation.
	if rec.Metadata.LatestUserPrompt != "" || rec.Metadata.LatestAssistantUpdate != "" ||
		!rec.Metadata.LatestUserPromptAt.IsZero() || rec.Metadata.ConversationCheckpointUnsettled {
		return false
	}
	switch rec.Metadata.ConversationCheckpointState {
	case "", domain.ConversationCheckpointLegacy, domain.ConversationCheckpointEmpty:
		// Blank legacy rows and an explicit empty checkpoint can accompany the
		// independent untouched-terminal / empty-Chat proof below.
	default:
		return false
	}
	if rec.Metadata.ConversationCheckpointGeneration != "" || rec.Metadata.ConversationCheckpointNativeID != "" {
		return false
	}
	if path := rec.Metadata.NativeTranscriptPath; path != "" {
		// SessionStart may announce a filename before creating the transcript;
		// that hint can survive a fresh switch back to Chat. Only a definitely
		// absent absolute path can accompany the mode-specific freshness proof.
		// Existing entries (including symlinks) and lookup errors block it.
		if !filepath.IsAbs(path) {
			return false
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if rec.Metadata.Prompt != "" {
			return false
		}
		store, ok := m.store.(chatHandoffHistoryStore)
		if !ok {
			return false
		}
		// The sequence is monotonic even when turns are rolled back or hidden.
		// Zero proves no message or activity was ever accepted; an empty visible
		// timeline would not. Startup settings do not consume this sequence.
		conversation, err := store.ConversationForSession(ctx, rec.ID)
		switch {
		case errors.Is(err, domain.ErrNoConversation):
			// The Chat lifecycle never began: no conversation row means no
			// branches, turns, or provider history could exist for this
			// session. A switch into Chat materializes that row only once the
			// controller starts, so its absence is fresh, not broken. Any other
			// read failure stays fail-closed below.
			return true
		case err != nil || conversation.SessionID != rec.ID || conversation.LatestSequence != 0:
			return false
		}
		branch, err := store.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
		// A project root's session_id records the orchestrator that created it;
		// after a rebind the owner is the conversation's current session.
		if err != nil || branch.ParentBranchID != "" ||
			(branch.SessionID != rec.ID && conversation.Scope != domain.ConversationScopeProject) ||
			branch.ProviderConversationID != rec.Metadata.ProviderConversationID {
			return false
		}
		// A provider can announce a turn before its first message/activity takes
		// a sequence. Include those turns, even if they were later hidden.
		hasTurns, err := store.HasConversationTurns(ctx, conversation.ID)
		return err == nil && !hasTurns
	}
	if _, ok := agent.(ports.AgentInterfaceHandoffHistoryProbe); !ok {
		return false
	}
	return m.terminalProvesNativeConversationNotStarted(ctx, rec, agent)
}

func (m *Manager) terminalProvesNativeConversationNotStarted(
	ctx context.Context,
	rec domain.SessionRecord,
	agent ports.Agent,
) bool {
	inspector, ok := agent.(ports.TerminalSurfaceInspector)
	if !ok {
		return false
	}
	styled, ok := m.runtime.(ports.StyledTerminalOutputReader)
	if !ok || strings.TrimSpace(rec.Metadata.RuntimeHandleID) == "" {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, interfaceInterruptSettle)
	defer cancel()
	output, err := styled.GetStyledOutput(
		probeCtx,
		ports.RuntimeHandle{ID: rec.Metadata.RuntimeHandleID},
		interfaceTransitionOutputLines,
	)
	if err != nil {
		return false
	}
	return inspector.InspectTerminalSurface(output).NativeConversationNotStarted
}

// persistedNativeConversationID verifies that a reserved native id has durable
// provider history behind it. A failed lookup is not proof of freshness: only
// positive untouched-terminal or durable empty-Chat proof may start fresh.
func (m *Manager) persistedNativeConversationID(
	ctx context.Context,
	rec domain.SessionRecord,
	id string,
	handoff ports.AgentInterfaceHandoff,
) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", nil
	}
	probe, ok := handoff.(ports.AgentInterfaceHandoffHistoryProbe)
	if !ok {
		return id, nil
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return "", err
	}
	env := m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env)
	exists, err := probe.NativeConversationExists(ctx, ports.SessionRef{
		ID:            string(rec.ID),
		WorkspacePath: rec.Metadata.WorkspacePath,
		Metadata: map[string]string{
			ports.MetadataKeyAgentSessionID: rec.Metadata.AgentSessionID,
			ports.MetadataKeySessionMode:    string(rec.Mode),
		},
	}, id, env)
	if err != nil {
		return "", fmt.Errorf("inspect native conversation %s: %w", id, err)
	}
	if !exists {
		agent, registered := m.agents.Agent(rec.Harness)
		if registered && m.nativeConversationNotStarted(ctx, rec, agent) {
			return "", nil
		}
		return "", fmt.Errorf("%w for %s", ErrNativeConversationMissing, rec.Harness)
	}
	return id, nil
}

func (m *Manager) preflightInterfaceTarget(
	ctx context.Context,
	rec domain.SessionRecord,
	transition domain.SessionInterfaceTransition,
) error {
	if transition.TargetMode == domain.SessionModeChat {
		if m.chat == nil {
			return ports.ErrChatUnsupported
		}
		project, err := m.loadProject(ctx, rec.ProjectID)
		if err != nil {
			return err
		}
		decision := m.decideFor(ctx, rec)
		permissions := permissioncap.Of(decision).Lower(rec.Harness, effectiveAgentConfigFor(rec.Harness, rec.Kind, rec.Role, project.Config).Permissions)
		return m.chat.PreflightChat(ctx, rec.Harness, permissions, decision)
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return harnessLookupError(rec.Harness)
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return err
	}
	decision := m.decideFor(ctx, rec)
	config := restoredAgentConfigUnder(decision, rec, project.Config)
	env := m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env)
	pinRuntimePermissionEnv(env, config.Permissions)
	m.augmentAgentRuntimeEnv(agent, env)
	if err := m.preflightLaunchAuth(ctx, agent, rec, env); err != nil {
		return err
	}
	return m.preflightTerminalLaunch(ctx, decision, agent, rec, project, transition.NativeConversationID)
}

// preflightLaunchAuth refuses a target whose login the provider reports as
// unauthorized in the exact launch environment. An inconclusive probe passes.
func (m *Manager) preflightLaunchAuth(ctx context.Context, agent ports.Agent, rec domain.SessionRecord, env map[string]string) error {
	validator, ok := agent.(ports.AgentLaunchAuthValidator)
	if !ok {
		return nil
	}
	status, authErr := validator.ValidateLaunchAuth(ctx, rec.Metadata.WorkspacePath, env)
	if authErr != nil {
		m.logger.Debug("interface transition: launch authentication probe inconclusive; continuing",
			"sessionID", rec.ID, "harness", rec.Harness, "error", authErr)
		return nil
	}
	if status == ports.AgentAuthStatusUnauthorized {
		return ports.ErrAgentAuthRequired
	}
	return nil
}

// preflightTerminalLaunch builds the terminal target's launch command, so a
// missing native conversation or binary fails before the source stops.
func (m *Manager) preflightTerminalLaunch(
	ctx context.Context,
	decision agentpolicy.Decision,
	agent ports.Agent,
	rec domain.SessionRecord,
	project domain.ProjectRecord,
	nativeConversationID string,
) error {
	if err := m.admitTerminalFence(decision, rec.Harness, restoredAgentConfigUnder(decision, rec, project.Config).Permissions); err != nil {
		return err
	}
	systemPrompt, err := m.buildSystemPromptFor(ctx, rec.Kind, rec.ProjectID, rec.ID, rec.RequestedOutput, rec.Role, false)
	if err != nil {
		return err
	}
	config := restoredAgentConfigUnder(decision, rec, project.Config)
	config.Launch = m.launchOptionsWithWindow(ctx, rec, project.Config, config.Model)
	var cmd []string
	if nativeConversationID == "" {
		cmd, _, _, err = freshLaunchArgv(ctx, decision, agent, rec.ID, rec.Metadata.WorkspacePath,
			rec.Metadata, systemPrompt, "", config, rec.Kind, m.dataDir, true, "", map[string]string{EnvRunFile: m.agentRunFilePath()})
	} else {
		var resumable bool
		cmd, resumable, err = agent.GetRestoreCommand(ctx, ports.RestoreConfig{
			Policy: decision,
			Session: ports.SessionRef{
				ID: string(rec.ID), WorkspacePath: rec.Metadata.WorkspacePath,
				Metadata: map[string]string{ports.MetadataKeyAgentSessionID: nativeConversationID},
			},
			Kind: rec.Kind, DataDir: m.dataDir, RunFilePath: m.agentRunFilePath(), SystemPrompt: systemPrompt,
			Config: config, Permissions: config.Permissions,
		})
		if err == nil && !resumable {
			return ErrNativeConversationMissing
		}
	}
	if err != nil {
		return err
	}
	return m.validateAgentBinary(cmd)
}
