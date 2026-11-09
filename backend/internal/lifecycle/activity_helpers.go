package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// waitPendingLaunch blocks while a pending launch with the signal's launch id
// is still being committed by MarkSpawned.
func (m *Manager) waitPendingLaunch(ctx context.Context, id domain.SessionID, launchID string) error {
	m.mu.Lock()
	for {
		pending, ok := m.pendingLaunches[id]
		if !ok || launchID == "" || pending.launchID != launchID {
			break
		}
		ready := pending.ready
		m.mu.Unlock()
		select {
		case <-ready:
			m.mu.Lock()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.mu.Unlock()
	return nil
}

// activityFence is the outcome of the ownership fences for one signal.
type activityFence struct {
	accepted              bool
	err                   error
	mode                  domain.SessionMode
	currentChatController bool
	nativeIdentityChanged bool
}

// fenceActivitySignal applies every ownership fence to the signal. A rejected
// signal returns accepted=false (with err set only on a store failure).
func (m *Manager) fenceActivitySignal(ctx context.Context, id domain.SessionID, rec domain.SessionRecord, s ports.ActivitySignal) activityFence {
	if rec.IsTerminated {
		m.dropSessionState(id)
		return activityFence{}
	}
	if s.ExpectedHarness != "" && s.ExpectedHarness != rec.Harness {
		return activityFence{}
	}
	mode := domain.NormalizeSessionMode(rec.Mode)
	// Rollback restores the TUI mode before its replacement runtime has a launch
	// generation. While the durable transition remains active, an untagged hook
	// can only belong to the stopped source runtime and must not claim this brief
	// ownerless epoch. Once relaunch commits RuntimeLaunchID, the normal generation
	// fence below takes over.
	if mode == domain.SessionModeTUI && rec.Metadata.RuntimeLaunchID == "" && s.LaunchID == "" {
		if reader, ok := m.store.(interfaceTransitionReader); ok {
			_, active, transitionErr := reader.GetActiveSessionInterfaceTransition(ctx, id)
			if transitionErr != nil {
				return activityFence{err: fmt.Errorf("lifecycle: read active interface transition: %w", transitionErr)}
			}
			if active {
				return activityFence{}
			}
		}
	}
	currentChatController := mode == domain.SessionModeChat &&
		s.ControllerGeneration != "" &&
		s.ControllerGeneration == rec.Metadata.ControllerGeneration
	// Controller ownership is mode-specific. A Chat controller has no terminal
	// launch id, so stale TUI metadata must not veto its current generation.
	// Provider hooks inherited from a shell never receive that internal
	// credential and therefore cannot mutate structured Chat lifecycle facts.
	// In TUI, a tagged callback must name the current launch; clearing that
	// launch during a handoff does not authorize the stopped source's callbacks.
	if mode != domain.SessionModeChat &&
		(s.LaunchID != "" || rec.Metadata.RuntimeLaunchID != "") &&
		s.LaunchID != rec.Metadata.RuntimeLaunchID {
		return activityFence{}
	}
	if (mode == domain.SessionModeChat && !currentChatController) ||
		(mode != domain.SessionModeChat && s.ControllerGeneration != "") {
		return activityFence{}
	}
	if s.ExpectedRevision != nil && rec.Revision != *s.ExpectedRevision {
		return activityFence{}
	}
	previousNativeID := rec.Metadata.AgentSessionID
	if previousNativeID == "" {
		previousNativeID = rec.Metadata.ProviderConversationID
	}
	// dsh's hooks plugin reports a child agent's own prompt, tool and Stop hooks
	// with the CHILD's session_id and no agent_id. Once this launch has proven the
	// root id, a different id in the same launch is a child: dropping it keeps a
	// child's Stop from idling the parent or replacing its resume id.
	if rec.Harness == domain.HarnessDeepSeek && s.SubagentID == "" && s.AgentSessionID != "" &&
		rec.Metadata.AgentSessionID != "" && s.AgentSessionID != rec.Metadata.AgentSessionID &&
		s.LaunchID != "" && s.LaunchID == rec.Metadata.AgentSessionIDLaunchID {
		return activityFence{}
	}
	nativeIdentityChanged := s.AgentSessionID != "" && previousNativeID != "" && s.AgentSessionID != previousNativeID
	if nativeIdentityChanged && !s.Timestamp.IsZero() &&
		!rec.Metadata.NativeIdentityObservedAt.IsZero() && !s.Timestamp.After(rec.Metadata.NativeIdentityObservedAt) {
		return activityFence{}
	}
	return activityFence{accepted: true, mode: mode, currentChatController: currentChatController, nativeIdentityChanged: nativeIdentityChanged}
}

// reduceActivityCheckpoint folds the signal into a copy of the conversation
// checkpoint and returns it with the (possibly stop-adjusted) signal.
func reduceActivityCheckpoint(rec domain.SessionRecord, s ports.ActivitySignal, mode domain.SessionMode, nativeIdentityChanged bool, now time.Time) (domain.SessionMetadata, ports.ActivitySignal, bool) {
	// Conversation text is meaningful only inside one provider identity, owner
	// generation, and main turn. Reduce it as one durable state machine so a Stop
	// whose UserPromptSubmit was lost can never borrow the prior turn's prompt.
	checkpoint := rec.Metadata
	resetConversationCheckpoint :=
		nativeIdentityChanged ||
			(s.AgentSessionID != "" && s.LaunchID != "" &&
				s.LaunchID != rec.Metadata.AgentSessionIDLaunchID) ||
			(s.Event == "session-start" && s.LaunchID != "" &&
				s.LaunchID != rec.Metadata.AgentSessionIDLaunchID)
	if resetConversationCheckpoint {
		checkpoint.LatestUserPrompt = ""
		checkpoint.LatestAssistantUpdate = ""
		checkpoint.LatestAssistantUpdateAt = time.Time{}
		checkpoint.ConversationCheckpointState = domain.ConversationCheckpointEmpty
		checkpoint.ConversationCheckpointGeneration = ""
		checkpoint.ConversationCheckpointNativeID = ""
		checkpoint.ConversationCheckpointTurnID = ""
		if nativeIdentityChanged {
			checkpoint.NativeTranscriptPath = ""
			checkpoint.ConversationCheckpointUnsettled = false
			checkpoint.NativeCheckpointEvidence = ""
		}
	}
	ownerGeneration := ""
	switch mode {
	case domain.SessionModeTUI:
		if s.LaunchID != "" {
			ownerGeneration = s.LaunchID
		}
	case domain.SessionModeChat:
		if s.ControllerGeneration != "" {
			ownerGeneration = s.ControllerGeneration
		}
	}
	checkpointNativeID := s.AgentSessionID
	if checkpointNativeID == "" && ownerGeneration != "" &&
		rec.Metadata.AgentSessionIDLaunchID == ownerGeneration {
		checkpointNativeID = rec.Metadata.AgentSessionID
	}
	claudeNativeBoundary := rec.Harness == domain.HarnessClaudeCode && mode == domain.SessionModeTUI &&
		ownerGeneration != "" && checkpointNativeID != "" && (s.Event == "user-prompt-submit" || s.Event == "stop")
	if claudeNativeBoundary {
		if checkpoint.NativeCheckpointEvidence == "" && !nativeIdentityChanged &&
			(rec.Metadata.ConversationCheckpointState.Trusted() || rec.Metadata.ConversationCheckpointUnsettled) {
			// A pre-upgrade pending/ambiguous boundary has no retained native witness.
			// Starting a new evidence journal must not silently discharge that debt.
			checkpoint.NativeCheckpointEvidence = `{"invalid":true}`
		}
		text := s.LatestAssistantUpdate
		if s.Event == "user-prompt-submit" {
			text = s.LatestUserPrompt
		}
		checkpoint.NativeCheckpointEvidence = domain.AppendNativeCheckpoint(
			checkpoint.NativeCheckpointEvidence, checkpointNativeID, domain.NativeCheckpointObservation{
				Generation: ownerGeneration, PromptID: s.ProviderTurnID, Submission: s.Event == "user-prompt-submit", SubmissionID: s.SubmissionID,
				Text: text, Coordination: s.ConversationCheckpointOrigin == domain.ConversationCheckpointOriginCoordination,
			})
	}
	acceptedPrompt := false
	owner := checkpointOwner{harness: rec.Harness, generation: ownerGeneration, nativeID: checkpointNativeID, reset: resetConversationCheckpoint, claudeBoundary: claudeNativeBoundary}
	switch s.Event {
	case "user-prompt-submit":
		acceptedPrompt = applyPromptSubmitCheckpoint(&checkpoint, s, owner, now)
	case "stop":
		applyStopCheckpoint(&checkpoint, &s, owner, now)
	}
	return checkpoint, s, acceptedPrompt
}

// activityMetadataChanged reports whether the signal or the reduced checkpoint
// changes any durable metadata on the row.
func activityMetadataChanged(rec domain.SessionRecord, s ports.ActivitySignal, checkpoint domain.SessionMetadata, subagentFactsChanged bool) bool {
	checkpointChanged := checkpoint.LatestUserPrompt != rec.Metadata.LatestUserPrompt ||
		!checkpoint.LatestAssistantUpdateAt.Equal(rec.Metadata.LatestAssistantUpdateAt) ||
		!checkpoint.LatestUserPromptAt.Equal(rec.Metadata.LatestUserPromptAt) ||
		checkpoint.LatestAssistantUpdate != rec.Metadata.LatestAssistantUpdate ||
		checkpoint.ConversationCheckpointState != rec.Metadata.ConversationCheckpointState ||
		checkpoint.ConversationCheckpointGeneration != rec.Metadata.ConversationCheckpointGeneration ||
		checkpoint.ConversationCheckpointNativeID != rec.Metadata.ConversationCheckpointNativeID ||
		checkpoint.ConversationCheckpointTurnID != rec.Metadata.ConversationCheckpointTurnID ||
		checkpoint.NativeCheckpointEvidence != rec.Metadata.NativeCheckpointEvidence ||
		checkpoint.ConversationCheckpointUnsettled != rec.Metadata.ConversationCheckpointUnsettled
	return (s.AgentSessionID != "" && rec.Metadata.AgentSessionID != s.AgentSessionID) ||
		(s.AgentSessionID != "" && s.Timestamp.After(rec.Metadata.NativeIdentityObservedAt)) ||
		(s.AgentSessionID != "" && rec.Metadata.AgentSessionIDLaunchID != s.LaunchID) ||
		(s.TranscriptPath != "" && rec.Metadata.NativeTranscriptPath != s.TranscriptPath) ||
		checkpointChanged || subagentFactsChanged
}

// afterActivityCommit runs the post-commit turn bookkeeping once the session
// lock is released.
func (m *Manager) afterActivityCommit(ctx context.Context, next domain.SessionRecord, prevState domain.ActivityState) {
	if prevState != next.Activity.State {
		m.noteTurnActivity(next.ID, prevState, next.Activity.State)
	}
	if prevState != domain.ActivityIdle && next.Activity.State == domain.ActivityIdle {
		m.operationGateMu.RLock()
		gate := m.operationGate
		m.operationGateMu.RUnlock()
		// A session manager that moves sessions to a new release hears of every
		// turn that ends. It must not block.
		if ender, ok := gate.(interface{ SessionTurnEnded(domain.SessionRecord) }); ok {
			ender.SessionTurnEnded(next)
		}
		m.notifyOrchestratorIdle(next)
		// Review comments held back during the turn are due now.
		m.deliverDeferredAfterTurn(ctx, next.ID)
	}
}

// writeActivityProjection persists next, choosing the turn-fallback writer when
// the signal retires a prompt or is eligible for a terminal fallback.
func (m *Manager) writeActivityProjection(ctx context.Context, rec, next domain.SessionRecord, originalSignal ports.ActivitySignal, observedRevision int64, acceptedTUIPrompt bool, now time.Time) (bool, error) {
	if writer, ok := m.store.(interface {
		UpdateSessionWithTurnFallback(context.Context, domain.SessionRecord, int64, domain.TurnFallback, bool) (bool, error)
	}); ok {
		retire := acceptedTUIPrompt && next.Kind == domain.KindWorker &&
			originalSignal.SubagentID == "" && !originalSignal.IntentionalStop
		switch originalSignal.NativeEntrypoint {
		case "sdk-cli", "sdk-ts", "sdk-py":
			retire = false
		}
		fallback, eligible := terminalFallback(rec, next, originalSignal, now)
		if originalSignal.Event == "user-prompt-submit" && !retire {
			eligible = false
		}
		if eligible || retire {
			return writer.UpdateSessionWithTurnFallback(ctx, next, observedRevision, fallback, retire)
		}
	}
	return m.store.UpdateSessionFromActivitySignal(ctx, next, observedRevision)
}

// exitedWorkloadResurrection reports a callback that must not revive an exited workload.
// An explicit prompt submission or authoritative liveness probe is proof
// that an agent remains alive in the preserved shell. Other same-generation
// callbacks may have been delayed behind the process-exit report and cannot
// resurrect an exited workload.
func exitedWorkloadResurrection(rec domain.SessionRecord, s ports.ActivitySignal, currentChatController bool) bool {
	return rec.Activity.State == domain.ActivityExited && (s.Valid || s.SubagentID != "") && s.State != domain.ActivityExited &&
		(s.State != domain.ActivityActive || s.Event != "user-prompt-submit") && !currentChatController &&
		s.Event != "liveness-probe"
}

// storeSubagentFacts writes the reduced subagent facts into the checkpoint for the
// session's harness and returns the previously stored facts.
func storeSubagentFacts(rec domain.SessionRecord, checkpoint *domain.SessionMetadata, subagentFacts string) string {
	storedSubagentFacts := ""
	switch rec.Harness {
	case domain.HarnessClaudeCode, domain.HarnessDeepSeek:
		storedSubagentFacts = rec.Metadata.ClaudeActivityFacts
		checkpoint.ClaudeActivityFacts = subagentFacts
	case domain.HarnessCodex:
		storedSubagentFacts = rec.Metadata.CodexActivityFacts
		checkpoint.CodexActivityFacts = subagentFacts
	}
	return storedSubagentFacts
}

// completedIdleTurnProof reports whether a same-state signal still must be written.
// A same-state repeat is still a write when it is the FIRST signal for
// this spawn: the receipt itself is a durable fact (it clears the
// no_signal display status). Hook deliveries are best-effort, so the
// first to ARRIVE may match the seeded state — e.g. a turn's "active"
// POST is lost and its Stop hook lands idle on the idle-seeded row.
// Some native CLIs only emit a post-agent boundary for tool-free turns.
// A scoped, newer completion is fresh idle proof even when the durable
// state stayed idle; otherwise terminal input permanently outlives it.
// Keep ordinary same-state notifications and unowned callbacks deduplicated.
func completedIdleTurnProof(rec domain.SessionRecord, s ports.ActivitySignal, act domain.Activity) bool {
	return s.State == domain.ActivityIdle && s.Event == "post-agent" &&
		s.LaunchID != "" && s.LaunchID == rec.Metadata.RuntimeLaunchID &&
		s.AgentSessionID != "" && s.AgentSessionID == rec.Metadata.AgentSessionID &&
		act.LastActivityAt.After(rec.Activity.LastActivityAt)
}

// needsInputIntent returns the ping for a transition into the needs-input family, if any.
func needsInputIntent(rec, next domain.SessionRecord) *ports.NotificationIntent {
	// Transition into the needs-input family (waiting_input or blocked) pings
	// the user; an in-family escalation (waiting_input -> blocked) does not
	// re-notify — the user was already pinged once for this pause.
	if !rec.Activity.State.NeedsInput() && next.Activity.State.NeedsInput() && !next.IsTerminated {
		return &ports.NotificationIntent{
			Type:               domain.NotificationNeedsInput,
			SessionID:          next.ID,
			ProjectID:          next.ProjectID,
			CreatedAt:          next.Activity.LastActivityAt,
			SessionDisplayName: next.DisplayName,
		}
	}
	return nil
}

// activityProjectionRetry decides whether a lost projection may retry: only a
// revision miss retries, and only within the contention budget.
func (m *Manager) activityProjectionRetry(ctx context.Context, id domain.SessionID, observedRevision int64, projectionAttempts int, event string) (bool, error) {
	// Only revision misses may retry; unchanged ownership fences still reject
	// the signal. Restore tool correlation before reducing the same signal again.
	current, found, err := m.store.GetSession(ctx, id)
	if err != nil || !found || current.Revision == observedRevision {
		return false, err
	}
	if projectionAttempts >= maxActivitySignalProjectionRetries {
		slog.Default().Warn("lifecycle: activity projection contention", "session", id, "event", event, "attempts", projectionAttempts+1)
		return false, fmt.Errorf("%w for %s: concurrent session writes exhausted %d attempts", ports.ErrActivityProjectionContention, id, projectionAttempts+1)
	}
	return true, nil
}

// activityProjection carries the per-attempt facts a projection write needs to
// roll tool correlation back and to decide whether a lost write may retry.
type activityProjection struct {
	id                domain.SessionID
	rec               *domain.SessionRecord // by reference: later metadata folds into rec must be visible to the write
	signal            ports.ActivitySignal
	event             string
	observedRevision  int64
	acceptedTUIPrompt bool
	now               time.Time
	toolFlight        *toolFlight
	steps             []domain.SessionStep
	attempts          *int
}

// project writes next; on a lost write it restores tool correlation and
// reports whether the same signal should be reduced again.
func (m *Manager) project(ctx context.Context, p activityProjection, next domain.SessionRecord) (applied, retry bool, err error) {
	applied, err = m.writeActivityProjection(ctx, *p.rec, next, p.signal, p.observedRevision, p.acceptedTUIPrompt, p.now)
	if applied {
		return applied, false, err
	}
	m.mu.Lock()
	m.restoreToolFlightLocked(p.id, p.toolFlight)
	m.restoreStepsLocked(p.id, p.steps)
	m.mu.Unlock()
	if err != nil {
		return false, false, err
	}
	retry, err = m.activityProjectionRetry(ctx, p.id, p.observedRevision, *p.attempts, p.event)
	if retry {
		*p.attempts++
	}
	return false, retry, err
}

// recordActivityTurn records the terminal turn (TUI) and last assistant message
// once the ownership fences have accepted the signal.
func (m *Manager) recordActivityTurn(ctx context.Context, rec domain.SessionRecord, s ports.ActivitySignal, mode domain.SessionMode, now time.Time) {
	if mode == domain.SessionModeTUI {
		m.recordTerminalTurn(ctx, rec, s, now)
	}
	m.recordLastAssistantMessage(ctx, rec, s, mode, now)
}

// dropFlightOnAgentExit forgets tool correlation when the agent process exits.
func (m *Manager) dropFlightOnAgentExit(id domain.SessionID, s ports.ActivitySignal) {
	if s.State != domain.ActivityExited {
		return
	}
	// The agent process can exit while the managed tmux session remains
	// alive and inspectable. Do not infer session termination from this
	// hook; a runtime observation or explicit lifecycle action owns that
	// fact. No tool/permission correlation survives an agent process exit.
	m.dropToolFlight(id)
}
