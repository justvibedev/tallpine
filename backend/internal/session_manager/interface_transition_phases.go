package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// errTransitionHandled marks a phase that already settled the durable
// transition (recovery or retained target), so the saga must not also fail it.
var errTransitionHandled = errors.New("interface transition already settled")

// transitionSaga is the mutable state of one runInterfaceTransition worker.
// Each phase returns a failure code and error; a nil error continues to the next
// phase, errTransitionHandled stops without a further fail.
type transitionSaga struct {
	m                    *Manager
	transition           domain.SessionInterfaceTransition
	rec                  domain.SessionRecord
	sourcePrepared       bool
	sourceStopped        bool
	modeChanged          bool
	accountStarting      bool // target may have started under the new account; rollback stops it first
	accountOverrideSet   bool
	sourceRuntimeHandle  ports.RuntimeHandle
	lastTerminalInputAt  time.Time
	releaseTerminalInput func()
}

// cleanup runs the worker-lifetime releases the single-function version deferred.
func (s *transitionSaga) cleanup() {
	if s.accountOverrideSet {
		s.m.clearAccountOverride(s.rec.ID)
	}
	if s.releaseTerminalInput != nil {
		s.releaseTerminalInput()
	}
}

func (s *transitionSaga) fail(code string, cause error) {
	m, transition := s.m, s.transition
	if s.sourcePrepared && !s.sourceStopped {
		m.abortSourceHandoff(transition)
	}
	if errors.Is(cause, context.Canceled) && !s.sourceStopped {
		_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionCancelled,
			"TRANSITION_CANCELLED", transitionDetail(transition, "TRANSITION_CANCELLED", "The interface switch was cancelled."))
		return
	}
	if s.sourceStopped {
		m.rollbackInterfaceTransition(transition, s.sourceRuntimeHandle, s.modeChanged, s.accountStarting, code, cause)
		return
	}
	_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionFailed, code,
		transitionDetail(transition, code, cause.Error()))
}

// move advances the durable phase, reporting TRANSITION_STATE_FAILED on error.
func (s *transitionSaga) move(phase domain.SessionInterfaceTransitionPhase) (string, error) {
	if err := s.m.moveInterfaceTransition(s.transition.ID, phase, "", ""); err != nil {
		return "TRANSITION_STATE_FAILED", err
	}
	return "", nil
}

// admit loads the session, checks it is still the source, and claims the raw
// terminal input path before target preflight or the first idle observation.
// Without this gate a mux client can submit work after the TUI is observed idle
// but before Destroy, and that accepted work is then killed by the handoff. Chat
// intake has its equivalent gate in BeginHandoff during drain.
func (s *transitionSaga) admit(ctx context.Context) (string, error) {
	if code, err := s.move(domain.SessionInterfaceTransitionPreflighting); err != nil {
		return code, err
	}
	rec, ok, err := s.m.store.GetSession(ctx, s.transition.SessionID)
	if err != nil || !ok {
		if err == nil {
			err = ErrNotFound
		}
		return "SESSION_NOT_FOUND", err
	}
	if rec.IsTerminated || domain.NormalizeSessionMode(rec.Mode) != s.transition.SourceMode {
		return "SESSION_CHANGED", fmt.Errorf("session changed before the interface switch could start")
	}
	s.rec = rec
	s.sourceRuntimeHandle = runtimeHandle(rec.Metadata)
	s.lastTerminalInputAt, s.releaseTerminalInput = s.m.beginTerminalInputDrain(rec)
	return "", nil
}

func (s *transitionSaga) preflight(ctx context.Context) (string, error) {
	preflight := s.m.preflightInterfaceTarget
	if s.transition.AccountMove() {
		preflight = s.m.preflightAccountTarget
	}
	if err := preflight(ctx, s.rec, s.transition); err != nil {
		return interfaceTransitionErrorCode(err), err
	}
	return "", nil
}

// drain quiesces the source, then re-resolves the native conversation.
func (s *transitionSaga) drain(ctx context.Context) (string, error) {
	if code, err := s.move(domain.SessionInterfaceTransitionDraining); err != nil {
		return code, err
	}
	if err := s.m.prepareSourceHandoff(ctx, s.rec, s.transition.Policy, s.lastTerminalInputAt); err != nil {
		return drainFailureCode(err), err
	}
	s.sourcePrepared = true
	return s.refreshNativeConversation(ctx)
}

func drainFailureCode(err error) string {
	switch {
	case errors.Is(err, errDrainQuiescenceUnverified):
		return "DRAIN_QUIESCENCE_UNVERIFIED"
	case errors.Is(err, errDrainDecisionPending):
		return "DRAIN_DECISION_PENDING"
	}
	return "SOURCE_QUIESCE_FAILED"
}

// refreshNativeConversation re-resolves every TUI conversation after input is
// frozen and drain is complete. A promptless TUI may create its first
// conversation, and an existing provider may change identity (for example after
// /clear) between admission and the gate. The target must receive the current
// identity, never the stale admission snapshot. A promptless Chat source must
// likewise prove it is still untouched after fencing, or transfer its newly
// created native ID.
func (s *transitionSaga) refreshNativeConversation(ctx context.Context) (string, error) {
	transition, rec := s.transition, s.rec
	if transition.SourceMode != domain.SessionModeTUI && transition.NativeConversationID != "" {
		return "", nil
	}
	current, found, refreshErr := s.m.store.GetSession(ctx, rec.ID)
	if refreshErr != nil || !found {
		if refreshErr == nil {
			refreshErr = ErrNotFound
		}
		return "SESSION_NOT_FOUND", refreshErr
	}
	if current.Metadata.RuntimeLaunchID != rec.Metadata.RuntimeLaunchID ||
		current.Metadata.ControllerGeneration != rec.Metadata.ControllerGeneration ||
		domain.NormalizeSessionMode(current.Mode) != transition.SourceMode {
		return "SESSION_CHANGED", fmt.Errorf("session changed while the interface switch was draining")
	}
	resolvedID, handoff, refreshErr := s.m.nativeConversationID(ctx, current)
	if refreshErr == nil {
		resolvedID, refreshErr = s.m.persistedNativeConversationID(ctx, current, resolvedID, handoff)
	}
	if refreshErr != nil {
		return interfaceTransitionErrorCode(refreshErr), refreshErr
	}
	if transition.HistoryPolicy == domain.SessionInterfaceTransitionHistoryProvider &&
		resolvedID != transition.NativeConversationID {
		recoveryErr := fmt.Errorf("%w: native conversation changed after recovery admission",
			ErrInterfaceProviderHistoryRecoveryUnavailable)
		return interfaceTransitionErrorCode(recoveryErr), recoveryErr
	}
	s.transition.NativeConversationID = resolvedID
	return "", nil
}

// stopSource commits source_stopping and stops the old controller.
// Cancellation is deliberately no longer inherited after source_stopping was
// committed. At this boundary the old process may already have received its
// stop signal; abandoning the operation could leave its liveness ambiguous.
// Retry the idempotent stop under fresh bounded contexts. If it still cannot be
// proven stopped, do not launch either controller: recovery is safer than two
// writers racing on one native conversation.
func (s *transitionSaga) stopSource(ctx context.Context) (string, error) {
	m, transition, rec := s.m, s.transition, s.rec
	if err := m.moveInterfaceTransitionWithNativeID(
		transition.ID,
		domain.SessionInterfaceTransitionSourceStopping,
		transition.NativeConversationID,
		"",
		"",
	); err != nil {
		return "TRANSITION_STATE_FAILED", err
	}
	if err := m.stopSourceControllerConclusive(rec); err != nil {
		detail := "Tallpine could not prove that the old controller stopped. Restart Tallpine to reconcile this session before sending more work: " + err.Error()
		_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionRecovery,
			"SOURCE_STOP_UNCERTAIN", detail)
		return "", errTransitionHandled
	}
	s.sourceStopped = true
	if code, err := s.move(domain.SessionInterfaceTransitionSourceStopped); err != nil {
		return code, err
	}
	return s.prepareHandoffStorage(ctx)
}

func (s *transitionSaga) prepareHandoffStorage(ctx context.Context) (string, error) {
	m, transition, rec := s.m, s.transition, s.rec
	bridge, ok := m.agents.Agent(rec.Harness)
	if !ok || transition.NativeConversationID == "" || transition.AccountMove() {
		return "", nil
	}
	storageBridge, supported := bridge.(ports.AgentInterfaceHandoffStorageBridge)
	if !supported {
		return "", nil
	}
	project, projectErr := m.loadProject(ctx, rec.ProjectID)
	if projectErr != nil {
		return "HANDOFF_STORAGE_BRIDGE_FAILED", projectErr
	}
	env := m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env)
	m.augmentAgentRuntimeEnv(bridge, env)
	if bridgeErr := storageBridge.PrepareInterfaceHandoffStorage(ctx, ports.SessionRef{
		ID: string(rec.ID), WorkspacePath: rec.Metadata.WorkspacePath,
		Metadata: map[string]string{ports.MetadataKeyAgentSessionID: transition.NativeConversationID},
	}, transition.SourceMode, transition.TargetMode, transition.NativeConversationID, env); bridgeErr != nil {
		return "HANDOFF_STORAGE_BRIDGE_FAILED", bridgeErr
	}
	return "", nil
}

// commitTarget moves the session onto the target: an account move installs the
// account override (the binding is committed only after the target is live, so
// a failure or daemon exit before then leaves the old account), a mode move
// commits the controller epoch.
func (s *transitionSaga) commitTarget(ctx context.Context) (string, error) {
	m, transition, rec := s.m, s.transition, s.rec
	if transition.AccountMove() {
		m.setAccountOverride(rec.ID, transition.TargetAccountID)
		s.accountOverrideSet = true
		s.accountStarting = true
	} else {
		if m.lcm == nil {
			return "LIFECYCLE_UNAVAILABLE", fmt.Errorf("lifecycle manager is unavailable")
		}
		changed, err := m.lcm.CommitControllerEpoch(ctx, rec.ID, transition.SourceMode,
			transition.TargetMode, transition.NativeConversationID, transition.NativeConversationID == "")
		if err != nil || !changed {
			if err == nil {
				err = fmt.Errorf("session mode compare-and-swap failed")
			}
			return "SESSION_CHANGED", err
		}
		s.modeChanged = true
	}
	return s.move(domain.SessionInterfaceTransitionTargetStarting)
}

// startTarget starts the target controller, retrying once on an unsettled ACP
// history snapshot.
func (s *transitionSaga) startTarget(ctx context.Context) (string, error) {
	m, transition, rec := s.m, s.transition, s.rec
	// Native-history replay is the TUI -> Chat barrier; an account move resumes
	// the same controller kind, so ordinary context resume applies.
	replayHistory := !transition.AccountMove()
	err := m.startTransitionTarget(ctx, rec.ID, transition.ID, transition.NativeConversationID == "", replayHistory, transition.HistoryPolicy)
	if shouldRetryUnsettledTarget(err, transition) {
		// An ACP history reader may expose an immutable snapshot for one provider
		// session. Its unsettled result is authoritative for that controller, but
		// not a permanent verdict on the native conversation: closing it and
		// starting the target once more obtains a fresh provider observation. Keep
		// the retry inside this durable transition, after the source was stopped,
		// so the source is not relaunched and two target controllers never overlap.
		// A provider that rejected the load outright is not retried: a second
		// target would spend another full settle budget on the same refusal.
		if stopErr := m.stopTransitionTargetConclusive(ctx, transition); stopErr != nil {
			_ = m.retainUnconfirmedTransitionTarget(transition, errors.Join(err, stopErr))
			return "", errTransitionHandled
		}
		err = m.startTransitionTarget(ctx, rec.ID, transition.ID, transition.NativeConversationID == "", replayHistory, transition.HistoryPolicy)
	}
	if err != nil {
		return targetStartFailureCode(err), err
	}
	return "", nil
}

func shouldRetryUnsettledTarget(err error, transition domain.SessionInterfaceTransition) bool {
	return errors.Is(err, ports.ErrChatHistoryUnsettled) &&
		!errors.Is(err, ports.ErrChatRecoveryInconclusive) &&
		!errors.Is(err, ports.ErrChatHistoryLoadFailed) &&
		len(ports.ChatHistoryMismatchDimensions(err)) == 0 && transition.TargetMode == domain.SessionModeChat
}

func targetStartFailureCode(err error) string {
	switch {
	case errors.Is(err, ports.ErrChatHistoryLoadFailed):
		return "TARGET_HISTORY_LOAD_FAILED"
	case errors.Is(err, ports.ErrChatHistoryUnavailable):
		return "TARGET_HISTORY_UNAVAILABLE"
	case ports.ChatHistoryMismatchOnlyUntrustedText(err):
		return "TARGET_HISTORY_UNTRUSTED_TEXT_MISMATCH"
	case errors.Is(err, ports.ErrChatHistoryUnsettled):
		return "TARGET_HISTORY_UNSETTLED"
	}
	return "TARGET_RESUME_FAILED"
}

// commitAccount binds the session to the new account once the new agent is live.
func (s *transitionSaga) commitAccount(ctx context.Context) (string, error) {
	if !s.transition.AccountMove() {
		return "", nil
	}
	m, transition, rec := s.m, s.transition, s.rec
	if err := m.agentAccounts.CommitSession(ctx, rec.ID, rec.Harness, transition.TargetAccountID); err != nil {
		return "ACCOUNT_BIND_FAILED", err
	}
	m.clearAccountOverride(rec.ID)
	s.accountOverrideSet = false
	s.accountStarting = false
	m.notifyAccountMoved(rec.Harness)
	m.markAccountSwitch(ctx, rec, transition)
	return "", nil
}

// activate drops the imported terminal turns and completes the transition.
func (s *transitionSaga) activate(ctx context.Context) (string, error) {
	m, transition, rec := s.m, s.transition, s.rec
	if transition.SourceMode == domain.SessionModeTUI && transition.TargetMode == domain.SessionModeChat &&
		transition.NativeConversationID != "" {
		// Chat resumed this provider thread and imported its history into the
		// conversation tables, so the terminal copy of those turns would now be a
		// duplicate. Turns of other threads were not imported and stay.
		if cleaner, ok := m.store.(interface {
			DeleteTerminalTurnsForThread(context.Context, domain.SessionID, domain.AgentHarness, string) error
		}); ok {
			if err := cleaner.DeleteTerminalTurnsForThread(ctx, rec.ID, rec.Harness, transition.NativeConversationID); err != nil {
				m.logger.Warn("delete imported terminal turns", "sessionID", rec.ID, "error", err)
			}
		}
	}
	if err := m.moveInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionActivating, "", ""); err != nil {
		// The target is live. Do not tear it down merely because diagnostics failed
		// to advance; mark recovery so a user is never left with no controller.
		_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionRecovery,
			"TRANSITION_STATE_FAILED", err.Error())
		return "", errTransitionHandled
	}
	if err := m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionCompleted, "", ""); err != nil {
		m.logger.Error("interface transition completed but durable completion failed",
			"sessionID", rec.ID, "transitionID", transition.ID, "error", err)
	}
	return "", nil
}
