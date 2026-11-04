package sessionmanager

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// recoverInterruptedInterfaceTransitions closes every durable handoff left
// active by a daemon exit. A TUI -> Chat transition whose mode commit landed is
// rolled back first: ordinary Chat restore is context-only and cannot satisfy
// the handoff's mandatory replay barrier. Explicit Resume can restore the source
// TUI, and a later retry performs native replay again idempotently. A failed
// target shutdown retains the same rollback obligation in either direction.
func (m *Manager) recoverInterruptedInterfaceTransitions(
	ctx context.Context,
) ([]domain.SessionInterfaceTransition, error) {
	return m.recoverInterfaceTransitions(ctx, "")
}

// A nonempty transitionID is an exact startup-captured recovery obligation.
// Deferred recovery must never sweep handoffs started after the API opened.
func (m *Manager) recoverInterfaceTransitions(ctx context.Context, transitionID string) ([]domain.SessionInterfaceTransition, error) {
	m.interfaceRecoveryMu.Lock()
	defer m.interfaceRecoveryMu.Unlock()
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return nil, nil
	}
	var active []domain.SessionInterfaceTransition
	var err error
	if transitionID == "" {
		active, err = store.ListActiveSessionInterfaceTransitions(ctx)
	} else {
		var transition domain.SessionInterfaceTransition
		var found bool
		transition, found, err = store.GetSessionInterfaceTransition(ctx, transitionID)
		if found && transition.Active() {
			active = append(active, transition)
		}
	}
	if err != nil {
		return nil, err
	}
	for i := range active {
		transition := &active[i]
		// Rebuild this session's input/reaper fence before serving or restoring
		// anything. An ambiguous target quarantines only its own session.
		m.agentOpMu.Lock()
		operation := m.agentOperations[transition.SessionID]
		if operation != "" && operation != agentOperationInterfaceRecovery {
			if m.deferredInterfaceRecovery == nil {
				m.deferredInterfaceRecovery = make(map[domain.SessionID]string)
			}
			m.deferredInterfaceRecovery[transition.SessionID] = transition.ID
			m.agentOpMu.Unlock()
			m.logger.Info("interface transition: recovery deferred behind exclusive operation", "sessionID", transition.SessionID, "operation", operation)
			continue
		}
		m.agentOperations[transition.SessionID] = agentOperationInterfaceRecovery
		delete(m.deferredInterfaceRecovery, transition.SessionID)
		drained := m.inputDrained[transition.SessionID]
		m.agentOpMu.Unlock()
		if drained != nil {
			select {
			case <-drained:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		detail, quarantined, err := m.recoverTransitionTarget(ctx, transition)
		if err != nil {
			return nil, err
		}
		if quarantined {
			continue
		}
		moved, err := store.AdvanceSessionInterfaceTransition(
			ctx,
			transition.ID,
			transition.Phase,
			domain.SessionInterfaceTransitionRecovery,
			transition.NativeConversationID,
			"DAEMON_RESTARTED",
			detail,
			m.clock(),
		)
		if err != nil {
			return nil, err
		}
		if !moved {
			// Deferred recovery runs after API admission. Cancellation may win
			// while the source is still intact; that durable result has no target
			// shutdown obligation and must not leave a permanent recovery fence.
			current, found, readErr := store.GetSessionInterfaceTransition(ctx, transition.ID)
			if readErr != nil {
				return nil, readErr
			}
			if found && current.Phase == domain.SessionInterfaceTransitionCancelled &&
				(transition.Phase == domain.SessionInterfaceTransitionRequested ||
					transition.Phase == domain.SessionInterfaceTransitionPreflighting ||
					transition.Phase == domain.SessionInterfaceTransitionDraining) {
				*transition = current
				m.endAgentOperation(transition.SessionID, agentOperationInterfaceRecovery)
				continue
			}
			return nil, fmt.Errorf("transition %s changed while recovering", transition.ID)
		}
		transition.Phase = domain.SessionInterfaceTransitionRecovery
		transition.ErrorCode = "DAEMON_RESTARTED"
		transition.ErrorDetail = detail
		transition.UpdatedAt = m.clock()
		transition.CompletedAt = transition.UpdatedAt
		m.endAgentOperation(transition.SessionID, agentOperationInterfaceRecovery)
	}
	return active, nil
}

// recoverTransitionTarget stops or rolls back a target left behind by the
// crashed transition and returns the recovery detail. quarantined is true when
// the target's shutdown could not be confirmed and the session stays fenced.
func (m *Manager) recoverTransitionTarget(ctx context.Context, transition *domain.SessionInterfaceTransition) (detail string, quarantined bool, err error) {
	detail = "The daemon restarted during the interface switch; Tallpine recovered the session from its last committed mode."
	if transition.AccountMove() {
		detail = "The daemon restarted during the account switch; the session stays on its committed account."
		switch transition.Phase {
		case domain.SessionInterfaceTransitionSourceStopped,
			domain.SessionInterfaceTransitionTargetStarting,
			domain.SessionInterfaceTransitionActivating:
			// A target may have started under the new account before its
			// binding committed. Stop it so nothing runs on an account the
			// session does not name; resume restarts it on the bound one.
			// A binding that already names the target is the commit, so the
			// running target is the session's agent and stays.
			rec, found, readErr := m.store.GetSession(ctx, transition.SessionID)
			if readErr != nil {
				return "", false, readErr
			}
			committed := false
			if found && m.agentAccounts != nil {
				bound, bindErr := m.agentAccounts.SessionAccountID(ctx, transition.SessionID, rec.Harness)
				if bindErr != nil {
					return "", false, bindErr
				}
				committed = bound == transition.TargetAccountID
			}
			if committed {
				detail = "The daemon restarted just after the account switch finished; the agent stays on its new account."
			} else if found && !rec.IsTerminated {
				if stopErr := m.stopTransitionTargetConclusive(ctx, *transition); stopErr != nil {
					if err := m.retainUnconfirmedTransitionTarget(*transition, stopErr); err != nil {
						return "", false, err
					}
					m.logger.Error("account transition: session quarantined after unconfirmed target shutdown", "sessionID", transition.SessionID, "transition", transition.ID, "error", stopErr)
					return "", true, nil
				}
				detail = "The daemon restarted during the account switch; Tallpine stopped the agent and kept the session on its committed account. Resume it to continue."
			}
		}
	} else if (transition.SourceMode == domain.SessionModeTUI && transition.TargetMode == domain.SessionModeChat) ||
		transition.ErrorCode == "TARGET_STOP_UNCONFIRMED" {
		rec, found, readErr := m.store.GetSession(ctx, transition.SessionID)
		if readErr != nil {
			return "", false, readErr
		}
		if found && !rec.IsTerminated && domain.NormalizeSessionMode(rec.Mode) == transition.TargetMode {
			if stopErr := m.stopTransitionTargetConclusive(ctx, *transition); stopErr != nil {
				if err := m.retainUnconfirmedTransitionTarget(*transition, stopErr); err != nil {
					return "", false, err
				}
				m.logger.Error("interface transition: session quarantined after unconfirmed target shutdown", "sessionID", transition.SessionID, "transition", transition.ID, "error", stopErr)
				return "", true, nil
			}
			if m.lcm == nil {
				return "", false, fmt.Errorf("recover transition %s: lifecycle manager is unavailable", transition.ID)
			}
			changed, rollbackErr := m.lcm.RestoreControllerEpoch(
				ctx,
				transition.SessionID,
				transition.TargetMode,
				transition.SourceMode,
				transition.NativeConversationID,
				transition.NativeConversationID == "",
			)
			if rollbackErr != nil {
				return "", false, fmt.Errorf("recover transition %s source mode: %w", transition.ID, rollbackErr)
			}
			if !changed {
				return "", false, fmt.Errorf("recover transition %s source mode: session changed", transition.ID)
			}
			if transition.SourceMode == domain.SessionModeTUI {
				detail = "The daemon restarted during the interface switch; Tallpine restored Terminal so native history can be replayed safely on retry."
			} else {
				detail = "The daemon restarted during the interface switch; Tallpine confirmed the failed Terminal controller stopped and restored Chat ownership."
			}
		}
	}
	return detail, false, nil
}
