package ports

import (
	"context"
	"errors"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// TerminalApprovalOption is one choice of a permission dialog drawn in a
// terminal UI. Index is its zero-based position in the list; Selected marks
// the row the cursor is on.
type TerminalApprovalOption struct {
	ID       string
	Label    string
	Kind     ChatDecisionKind
	Index    int
	Selected bool
}

// TerminalApproval is a live permission dialog read from a terminal screen.
// Summary is the dialog's headline and Command the exact request line.
type TerminalApproval struct {
	Summary string
	Command string
	Options []TerminalApprovalOption
}

// TerminalApprovalReader is an OPTIONAL capability of a TUI agent adapter
// whose permission dialogs are answered with arrow keys and Enter. It parses
// the dialog from captured pane output, or reports false when none is live.
type TerminalApprovalReader interface {
	ParseTerminalApproval(output string) (TerminalApproval, bool)
}

// PendingTerminalApproval is a live terminal dialog with its stable request id.
type PendingTerminalApproval struct {
	RequestID string
	Approval  TerminalApproval
}

// ErrTerminalApprovalNotPending means the dialog the caller named is no longer
// on the session's screen.
var ErrTerminalApprovalNotPending = errors.New("terminal approval is not pending")

// TerminalUsageReader is an OPTIONAL capability of a TUI agent adapter that
// can read a session's spend from the agent's own transcript, for harnesses
// whose terminal mode has no chat conversation to hold it. ok is false when
// no transcript for the native session id exists.
type TerminalUsageReader interface {
	TerminalModelUsage(ctx context.Context, agentSessionID string) (usage []domain.ModelUsage, ok bool, err error)
}
