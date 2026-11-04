package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (m *Manager) runInterfaceTransition(
	ctx context.Context,
	transition domain.SessionInterfaceTransition,
	run *interfaceTransitionRun,
) {
	defer m.settleTransitionMessages(transition)
	defer close(run.done)
	defer func() {
		m.transitionMu.Lock()
		if current := m.transitions[transition.SessionID]; current == run {
			delete(m.transitions, transition.SessionID)
		}
		m.transitionMu.Unlock()
	}()

	// Chat was armed synchronously before StartInterfaceTransition returned. TUI
	// installs its raw-input gate in admit because that gate is tied to this
	// worker's lifetime. Any failure before Chat stops must therefore reopen Chat
	// intake.
	saga := &transitionSaga{m: m, transition: transition, sourcePrepared: transition.SourceMode == domain.SessionModeChat}
	defer saga.cleanup()
	for _, phase := range []func(context.Context) (string, error){
		saga.admit, saga.preflight, saga.drain, saga.stopSource,
		saga.commitTarget, saga.startTarget, saga.commitAccount, saga.activate,
	} {
		if code, err := phase(ctx); err != nil {
			if !errors.Is(err, errTransitionHandled) {
				saga.fail(code, err)
			}
			return
		}
	}
}

func (m *Manager) startTransitionTarget(
	ctx context.Context,
	id domain.SessionID,
	transitionID string,
	fresh, requireNativeHistory bool,
	historyPolicy domain.SessionInterfaceTransitionHistoryPolicy,
) error {
	ctx, cancel := context.WithTimeout(ctx, interfaceTransitionStepLimit)
	defer cancel()
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return err
	}
	ws := workspaceInfo(rec)
	// A genuinely fresh target has nothing to replay. Every resumed TUI -> Chat
	// handoff must import native history before activation; rollback and ordinary
	// daemon restore deliberately use the normal context-resume policy.
	generation := ""
	if !fresh {
		generation, err = m.turnFallbackHandoffGeneration(ctx, rec, transitionID)
		if err != nil {
			return err
		}
	}
	_, err = m.relaunchSessionWithPolicyAndGeneration(
		ctx, "switch interface", rec, project, ws, nil,
		fresh, requireNativeHistory && !fresh, generation, historyPolicy, nil,
	)
	return err
}

func (m *Manager) moveInterfaceTransition(
	id string,
	next domain.SessionInterfaceTransitionPhase,
	errorCode, errorDetail string,
) error {
	return m.moveInterfaceTransitionWithNativeID(id, next, "", errorCode, errorDetail)
}

func (m *Manager) moveInterfaceTransitionWithNativeID(
	id string,
	next domain.SessionInterfaceTransitionPhase,
	nativeConversationID string,
	errorCode, errorDetail string,
) error {
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return ports.ErrChatUnsupported
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for attempt := 0; attempt < 4; attempt++ {
		current, ok, err := store.GetSessionInterfaceTransition(ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if current.Phase.Terminal() {
			return fmt.Errorf("transition %s is already %s", id, current.Phase)
		}
		if nativeConversationID == "" {
			nativeConversationID = current.NativeConversationID
		}
		moved, err := store.AdvanceSessionInterfaceTransition(ctx, id, current.Phase, next,
			nativeConversationID, errorCode, errorDetail, m.clock())
		if err != nil {
			return err
		}
		if moved {
			return nil
		}
	}
	return fmt.Errorf("transition %s changed concurrently", id)
}

func (m *Manager) finishInterfaceTransition(
	id string,
	phase domain.SessionInterfaceTransitionPhase,
	errorCode, errorDetail string,
) error {
	return m.moveInterfaceTransition(id, phase, errorCode, errorDetail)
}

func (m *Manager) retainUnconfirmedTransitionTarget(transition domain.SessionInterfaceTransition, cause error) error {
	// Keep the durable input fence and startup-recovery record. Terminalizing the
	// saga would let ordinary restore adopt a target that never passed admission.
	if err := m.moveInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionTargetStarting,
		"TARGET_STOP_UNCONFIRMED", "Tallpine could not confirm the target controller stopped. Restart Tallpine to retry shutdown before restoring the original interface. "+cause.Error()); err != nil {
		m.logger.Error("interface transition: retain unconfirmed target shutdown", "transition", transition.ID, "error", err)
		return fmt.Errorf("persist unconfirmed target for transition %s: %w", transition.ID, err)
	}
	return nil
}

func (m *Manager) stopTransitionTargetConclusive(ctx context.Context, transition domain.SessionInterfaceTransition) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	current, found, err := m.store.GetSession(ctx, transition.SessionID)
	if err != nil {
		return fmt.Errorf("read failed target ownership: %w", err)
	}
	if !found || domain.NormalizeSessionMode(current.Mode) != transition.TargetMode {
		return errors.New("failed target ownership changed before shutdown")
	}
	if transition.TargetMode == domain.SessionModeChat && m.chat == nil {
		return errors.New("cannot confirm target shutdown without Chat service")
	}
	return m.stopSourceControllerConclusive(current)
}

func (m *Manager) rollbackInterfaceTransition(
	transition domain.SessionInterfaceTransition,
	sourceRuntimeHandle ports.RuntimeHandle,
	modeChanged, accountStarting bool,
	code string,
	cause error,
) {
	ctx, cancel := context.WithTimeout(context.Background(), interfaceTransitionStepLimit)
	defer cancel()
	if accountStarting {
		// The target may be running under the new account. Stop it before the
		// source restarts under the old one, which the binding still names.
		if stopErr := m.stopTransitionTargetConclusive(ctx, transition); stopErr != nil {
			_ = m.retainUnconfirmedTransitionTarget(transition, errors.Join(cause, stopErr))
			return
		}
		m.clearAccountOverride(transition.SessionID)
	}
	if modeChanged {
		// A target may have partially started. Stop whatever its committed mode can
		// identify before restoring the old writer. A detached event stream is not
		// proof that a persistent provider host stopped.
		if stopErr := m.stopTransitionTargetConclusive(ctx, transition); stopErr != nil {
			_ = m.retainUnconfirmedTransitionTarget(transition, errors.Join(cause, stopErr))
			return
		}
		if m.lcm == nil {
			_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionRecovery,
				"RECOVERY_REQUIRED", cause.Error()+"; rollback mode: lifecycle manager is unavailable")
			return
		}
		changed, err := m.lcm.RestoreControllerEpoch(ctx, transition.SessionID,
			transition.TargetMode, transition.SourceMode, transition.NativeConversationID,
			transition.NativeConversationID == "")
		if err != nil || !changed {
			detail := cause.Error()
			if err != nil {
				detail += "; rollback mode: " + err.Error()
			}
			_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionRecovery,
				"RECOVERY_REQUIRED", detail)
			return
		}
	}
	// A conclusive liveness probe can prove the source process exited even when
	// its first teardown timed out. Clear any stale runtime registration using
	// the handle captured before the controller epoch erased it, or rollback can
	// fail to recreate the TUI with "session already exists" on ConPTY.
	if transition.SourceMode != domain.SessionModeChat && sourceRuntimeHandle.ID != "" {
		if err := m.runtime.Destroy(ctx, sourceRuntimeHandle); err != nil {
			_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionRecovery,
				"RECOVERY_REQUIRED", cause.Error()+"; source runtime cleanup: "+err.Error())
			return
		}
	}
	// A fresh relaunch drops the provider id only in memory. When the epoch never
	// committed, a Chat source still owns that id durably, so it must resume it:
	// a fresh start would present an owner the database does not hold. A Chat
	// whose controller never started holds no id, so it can only start fresh.
	fresh := transition.NativeConversationID == "" &&
		(modeChanged || transition.SourceMode != domain.SessionModeChat ||
			!m.chatHoldsProviderConversation(ctx, transition.SessionID))
	if err := m.startTransitionTarget(
		ctx,
		transition.SessionID,
		transition.ID,
		fresh,
		false,
		domain.SessionInterfaceTransitionHistoryStrict,
	); err != nil {
		_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionRecovery,
			"RECOVERY_REQUIRED", cause.Error()+"; source restore: "+err.Error())
		return
	}
	_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionFailed, code, cause.Error())
}

// chatHoldsProviderConversation reports whether the session durably holds a
// provider conversation id. A read failure counts as holding one, so rollback
// keeps resuming instead of starting fresh on uncertain evidence.
func (m *Manager) chatHoldsProviderConversation(ctx context.Context, id domain.SessionID) bool {
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil || !found {
		return true
	}
	return strings.TrimSpace(rec.Metadata.ProviderConversationID) != ""
}
