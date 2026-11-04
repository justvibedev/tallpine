package sessionmanager

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/tmuxbin"
)

// restoreArgv builds the argv to relaunch a torn-down session: the agent's
// native resume command when it can continue the session, else a fresh launch
// for harnesses where replaying the saved prompt is acceptable. The agent
// signals via ok=false (e.g. no native session id captured yet). Returns
// ErrNotResumable when transcript-preserving restore is required but unavailable,
// or when a promptless, unresumable worker has nothing to restore from.
func restoreArgv(ctx context.Context, decision agentpolicy.Decision, agent ports.Agent, id domain.SessionID, workspacePath string, meta domain.SessionMetadata, systemPrompt, systemPromptFile string, agentConfig ports.AgentConfig, kind domain.SessionKind, _ domain.AgentHarness, dataDir string, env map[string]string) ([]string, ports.PromptDeliveryStrategy, RestoreMode, error) {
	ref := ports.SessionRef{
		ID:            string(id),
		WorkspacePath: workspacePath,
		Metadata: map[string]string{
			ports.MetadataKeyAgentSessionID: meta.AgentSessionID,
			ports.MetadataKeySessionMode:    string(domain.SessionModeTUI),
		},
	}
	cmd, ok, err := agent.GetRestoreCommand(ctx, ports.RestoreConfig{Policy: decision, Session: ref, Kind: kind, DataDir: dataDir, RunFilePath: env[EnvRunFile], Env: env, SystemPrompt: systemPrompt, SystemPromptFile: systemPromptFile, Config: agentConfig, Permissions: agentConfig.Permissions})
	if err != nil {
		return nil, "", "", fmt.Errorf("restore command: %w", err)
	}
	// The id is real but the provider never persisted a conversation under it —
	// a session torn down before its first turn, or one whose switch away failed
	// after the source stopped. Resuming that id fails with "No conversation
	// found" every time, so the id alone must not gate restore.
	conversationLost := ok && nativeConversationMissing(ctx, agent, ref, meta.AgentSessionID, env)
	if ok && !conversationLost {
		return cmd, ports.PromptDeliveryInCommand, RestoreModeNative, nil
	}
	// A lost conversation is not the same as nothing to restore: the session
	// reached the point of reserving an id, and its workspace holds whatever
	// branch and commits it produced. Relaunch into that workspace even without
	// a saved prompt, rather than stranding the work behind ErrNotResumable. A
	// worker that never got that far (no id, no prompt) still stays unresumable.
	return freshLaunchArgv(ctx, decision, agent, id, workspacePath, meta, systemPrompt,
		systemPromptFile, agentConfig, kind, dataDir, conversationLost, "", env)
}

// nativeConversationMissing reports whether the agent can see a persisted
// conversation behind the id Tallpine would resume. Agents that cannot answer (no
// probe, or a failed probe) are treated as present: a restore that might work
// beats refusing one that would.
func nativeConversationMissing(ctx context.Context, agent ports.Agent, ref ports.SessionRef, conversationID string, env map[string]string) bool {
	probe, ok := agent.(ports.AgentInterfaceHandoffHistoryProbe)
	if !ok {
		return false
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		handoff, ok := agent.(ports.AgentInterfaceHandoff)
		if !ok {
			return false
		}
		var err error
		conversationID, ok, err = handoff.NativeConversationID(ctx, ref, domain.SessionModeTUI, "")
		if err != nil || !ok {
			return false
		}
		conversationID = strings.TrimSpace(conversationID)
		if conversationID == "" {
			return false
		}
	}
	exists, err := probe.NativeConversationExists(ctx, ref, conversationID, env)
	return err == nil && !exists
}

// freshLaunchArgv builds the non-resume half of restoreArgv. Interface
// transitions also use it when an adapter proves its reserved id has no
// persisted history, both for preflight and for the actual target launch.
func freshLaunchArgv(ctx context.Context, decision agentpolicy.Decision, agent ports.Agent, id domain.SessionID, workspacePath string, meta domain.SessionMetadata, systemPrompt, systemPromptFile string, agentConfig ports.AgentConfig, kind domain.SessionKind, dataDir string, allowPromptless bool, nativeSessionID string, env map[string]string) ([]string, ports.PromptDeliveryStrategy, RestoreMode, error) {
	// A saved prompt is replayed fresh. An orchestrator is promptless by design
	// and relaunches with the system prompt only. A promptless WORKER has no task
	// and no session id to restore from: do not blank-relaunch it.
	if meta.Prompt == "" && kind != domain.KindOrchestrator && !allowPromptless {
		return nil, "", "", ErrNotResumable
	}
	// Fall through to a fresh launch. Command-delivered agents receive
	// meta.Prompt in argv; after-start agents receive it via the messenger once
	// the runtime is live.
	launchCfg := ports.LaunchConfig{
		Policy:           decision,
		DataDir:          dataDir,
		SessionID:        string(id),
		WorkspacePath:    workspacePath,
		Kind:             kind,
		Prompt:           meta.Prompt,
		SystemPrompt:     systemPrompt,
		SystemPromptFile: systemPromptFile,
		Config:           agentConfig,
		Permissions:      agentConfig.Permissions,
		NativeSessionID:  nativeSessionID,
		// The fence (protectState) is built from the launch env: without the run
		// file in it a fresh Claude launch runs unsandboxed (B454).
		Env: env,
	}
	delivery, err := agent.GetPromptDeliveryStrategy(ctx, launchCfg)
	if err != nil {
		return nil, "", "", fmt.Errorf("prompt delivery: %w", err)
	}
	if delivery == ports.PromptDeliveryAfterStart {
		launchCfg.Prompt = ""
	}
	argv, err := agent.GetLaunchCommand(ctx, launchCfg)
	if err != nil {
		return nil, "", "", fmt.Errorf("launch command: %w", err)
	}
	mode := RestoreModeFresh
	if meta.Prompt != "" {
		mode = RestoreModeSavedPrompt
	}
	return argv, delivery, mode, nil
}

// validateAgentBinary checks that argv[0] resolves via the manager's
// lookPath (exec.LookPath in prod) before any runtime work happens. Adapters
// that can't resolve their binary now return ports.ErrAgentBinaryNotFound from
// GetLaunchCommand directly; this guard is a defense-in-depth for adapters
// that return an argv[0] like "claude" without verifying. Some adapters prefix
// their command with `env KEY=value`; in that case validate the first real
// executable after the environment assignments.
func (m *Manager) validateAgentBinary(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("agent: empty launch argv: %w", ports.ErrAgentBinaryNotFound)
	}
	bin, ok := agentlaunch.LaunchBinary(argv)
	if !ok {
		return fmt.Errorf("agent: launch argv missing binary: %w", ports.ErrAgentBinaryNotFound)
	}
	if _, err := m.lookPath(bin); err != nil {
		return fmt.Errorf("agent binary %q: %w", bin, ports.ErrAgentBinaryNotFound)
	}
	return nil
}

func (m *Manager) augmentRuntimePATHForLaunchBinary(ctx context.Context, env map[string]string, argv []string) {
	agentlaunch.AugmentRuntimePATHForLaunchBinary(ctx, env, argv, m.lookPath, agentlaunch.PinnedDir(m.executable, m.dataDir))
}

func boundedConversationFact(value string) string {
	const limit = 16 << 10
	if value = strings.TrimSpace(value); len(value) > limit {
		return strings.ToValidUTF8(value[:limit], "�")
	}
	return value
}

func (m *Manager) validateRuntimePrerequisites() error {
	if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
		return nil
	}
	if resolution, err := tmuxbin.ResolveWith(brand.Getenv("TMUX_BINARY"), m.executable, m.lookPath); err != nil || resolution.Path == "" {
		return fmt.Errorf("%w: tmux required on macOS but Tallpine's configured, bundled, or system tmux was not found", ports.ErrRuntimePrerequisite)
	}
	return nil
}
