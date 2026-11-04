package accountadopt

import (
	"errors"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

// fromTransition maps the move's durable phase to the session's outcome. A
// draining move is busy: it waits for the turn and no longer holds a slot.
func fromTransition(t domain.SessionInterfaceTransition) (domain.AccountAdoptionItemState, string, string) {
	switch t.Phase {
	case domain.SessionInterfaceTransitionCompleted:
		return domain.AccountItemMoved, "", ""
	case domain.SessionInterfaceTransitionFailed, domain.SessionInterfaceTransitionCancelled, domain.SessionInterfaceTransitionRecovery:
		code := t.ErrorCode
		if code == "" {
			code = "MOVE_" + string(t.Phase)
		}
		return domain.AccountItemFailed, code, t.ErrorDetail
	case domain.SessionInterfaceTransitionDraining:
		return domain.AccountItemWaiting, "", ""
	default:
		return domain.AccountItemMoving, "", ""
	}
}

// failure turns a refusal to start a move into the item's outcome.
func failure(err error) (domain.AccountAdoptionItemState, string, string) {
	switch {
	case errors.Is(err, ports.ErrAgentAccountUnchanged):
		return domain.AccountItemSkipped, "", ""
	case errors.Is(err, sessionmanager.ErrTerminated), errors.Is(err, sessionmanager.ErrNotFound):
		return domain.AccountItemSkipped, "", ""
	case errors.Is(err, sessionmanager.ErrInterfaceTransitionInProgress):
		return domain.AccountItemFailed, "SESSION_SWITCHING", err.Error()
	case errors.Is(err, ports.ErrAgentAccountSignedOut):
		return domain.AccountItemFailed, "AGENT_ACCOUNT_SIGNED_OUT", err.Error()
	case errors.Is(err, ports.ErrAgentAccountSplitHistory):
		return domain.AccountItemFailed, "AGENT_ACCOUNT_SPLIT_HISTORY", err.Error()
	case errors.Is(err, sessionmanager.ErrNativeConversationMissing):
		return domain.AccountItemFailed, "NATIVE_SESSION_MISSING", err.Error()
	case errors.Is(err, sessionmanager.ErrInterfaceWorkspaceUnavailable):
		return domain.AccountItemFailed, "WORKSPACE_UNAVAILABLE", err.Error()
	}
	return domain.AccountItemFailed, "MOVE_FAILED", err.Error()
}
