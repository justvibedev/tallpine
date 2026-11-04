package sessionmanager

import (
	"context"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// restoreLaunchPlan is the argv decision of one relaunch.
type restoreLaunchPlan struct {
	argv                 []string
	initialization       ports.NativeRestoreInitialization
	delivery             ports.PromptDeliveryStrategy
	mode                 RestoreMode
	freshNativeSessionID string
}

// checkRestoreLaunchAuth probes launch authentication; an inconclusive probe
// only logs, an explicit unauthorized status fails the restore.
func (m *Manager) checkRestoreLaunchAuth(ctx context.Context, agent ports.Agent, rec domain.SessionRecord, workspacePath string, env map[string]string) error {
	validator, ok := agent.(ports.AgentLaunchAuthValidator)
	if !ok {
		return nil
	}
	status, authErr := validator.ValidateLaunchAuth(ctx, workspacePath, env)
	if authErr != nil {
		m.logger.Debug("restore: launch authentication probe inconclusive; continuing",
			"sessionID", rec.ID, "harness", rec.Harness, "error", authErr)
	} else if status == ports.AgentAuthStatusUnauthorized {
		return ports.ErrAgentAuthRequired
	}
	return nil
}

// verifyNativeHistoryExists asks a history-probing adapter whether the stored
// native conversation can still be resumed.
func verifyNativeHistoryExists(ctx context.Context, agent ports.Agent, rec domain.SessionRecord, workspacePath string, env map[string]string) error {
	probe, ok := agent.(ports.AgentInterfaceHandoffHistoryProbe)
	if !ok {
		return nil
	}
	ref := ports.SessionRef{ID: string(rec.ID), WorkspacePath: workspacePath,
		Metadata: map[string]string{ports.MetadataKeyAgentSessionID: rec.Metadata.AgentSessionID}}
	exists, probeErr := probe.NativeConversationExists(ctx, ref, rec.Metadata.AgentSessionID, env)
	if probeErr != nil || !exists {
		return fmt.Errorf("native history could not be verified: %w", errors.Join(ErrNotResumable, probeErr))
	}
	return nil
}

// planRestoreLaunch chooses between a native restore, a fresh launch and the
// adapter's plain restore argv. A native restore may add launch env to env.
func (m *Manager) planRestoreLaunch(ctx context.Context, decision agentpolicy.Decision, agent ports.Agent, rec domain.SessionRecord, workspacePath, systemPrompt, systemPromptFile string, agentConfig ports.AgentConfig, env map[string]string, forceFresh, requireNativeHistory, hasPromptOverride bool) (restoreLaunchPlan, error) {
	var p restoreLaunchPlan
	var err error
	// Claude Code pins its native id to the Tallpine session and refuses the id an
	// earlier conversation used, so a handoff's fresh conversation gets a new one.
	if provider, ok := agent.(ports.AgentFreshNativeSessionIDProvider); ok && forceFresh && hasPromptOverride {
		p.freshNativeSessionID = provider.NewNativeSessionID()
	}
	fresh := func() {
		p.argv, p.delivery, p.mode, err = freshLaunchArgv(ctx, decision, agent, rec.ID, workspacePath, rec.Metadata,
			systemPrompt, systemPromptFile, agentConfig, rec.Kind, m.dataDir, true, p.freshNativeSessionID, env)
	}
	if restorer, ok := agent.(ports.AgentInteractiveNativeRestorer); ok && !forceFresh {
		identityUnverified := rec.Metadata.RuntimeLaunchID != "" && rec.Metadata.AgentSessionIDLaunchID != rec.Metadata.RuntimeLaunchID
		if identityUnverified && requireNativeHistory {
			err = ErrNotResumable
		} else if identityUnverified {
			fresh()
		} else {
			p.initialization, err = restorer.PrepareNativeRestore(ctx, ports.RestoreConfig{Policy: decision, Session: ports.SessionRef{ID: string(rec.ID), WorkspacePath: workspacePath, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: rec.Metadata.AgentSessionID}}, DataDir: m.dataDir, Kind: rec.Kind, Config: agentConfig, Permissions: agentConfig.Permissions, SystemPrompt: systemPrompt, SystemPromptFile: systemPromptFile})
			if err == nil {
				p.argv = p.initialization.Argv()
				for key, value := range p.initialization.LaunchEnv() {
					env[key] = value
				}
				p.delivery, p.mode = ports.PromptDeliveryInCommand, RestoreModeNative
			} else if errors.Is(err, ports.ErrNativeHistoryUnavailable) && !requireNativeHistory {
				fresh()
			}
		}
	} else if forceFresh {
		fresh()
	} else {
		p.argv, p.delivery, p.mode, err = restoreArgv(ctx, decision, agent, rec.ID, workspacePath, rec.Metadata,
			systemPrompt, systemPromptFile, agentConfig, rec.Kind, rec.Harness, m.dataDir, env)
	}
	return p, err
}

// verifyResumedLaunch waits until the exact resumed child is observable. A
// runtime accepting Restart only proves that its supervisor was created; the
// previous launch is kept until then, so a failed native resume stays
// recoverable.
func (m *Manager) verifyResumedLaunch(ctx context.Context, rec domain.SessionRecord, handle ports.RuntimeHandle, launchID string) error {
	var startErr error
	if rec.StaleBinary != nil {
		startErr = m.verifyRefreshedTerminal(ctx, handle, rec.ID, launchID)
	} else {
		alive, err := m.waitForExactLaunch(ctx, handle, rec.ID, launchID)
		if err != nil || !alive {
			startErr = errors.Join(ErrNotResumable, err)
		}
	}
	if startErr == nil {
		return nil
	}
	if rec.StaleBinary != nil && domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeTUI {
		// This replacement has not committed its launch ID, so its exit
		// hook is correctly fenced from the old row. Reconcile a proven
		// dead child explicitly: otherwise that row stays idle and Resume
		// (including the automatic retry) cannot recover it. Unknown
		// probes retain state without claiming that a process has died.
		probe := m.runtime.ProbeFencedRuntime(ctx, ports.FencedRuntimeRef{Handle: handle, SessionID: rec.ID, Generation: launchID})
		if probe.Liveness == ports.FencedDead {
			_, preserveErr := m.preserveFailedReconcileRelaunch(ctx, rec)
			startErr = errors.Join(startErr, preserveErr)
		}
	}
	return startErr
}
