package sessionmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ErrTerminalApprovalNotPending means the dialog the caller named is no longer
// on the session's screen (answered, replaced, or never drawn).
var ErrTerminalApprovalNotPending = ports.ErrTerminalApprovalNotPending

const (
	terminalApprovalLines = 60
	terminalKeyDown       = "\x1b[B"
	terminalKeyUp         = "\x1b[A"
	terminalKeyEnter      = "\r"
	terminalKeyPause      = 150 * time.Millisecond
)

// terminalApprovalLocks serializes answers per session id. An entry exists only
// while a resolve holds or waits for it, so the map does not grow with the
// number of sessions ever resolved.
var terminalApprovalLocks = newKeyedLocks[domain.SessionID]()

type runtimeInputSender interface {
	SendInput(context.Context, ports.RuntimeHandle, string) error
}

func terminalApprovalID(a ports.TerminalApproval) string {
	sum := sha256.Sum256([]byte(a.Summary + "\n" + a.Command))
	return "term-" + hex.EncodeToString(sum[:6])
}

// terminalCapability resolves a session to its record and the agent's optional
// terminal capability T. ok is false when the session is chat-mode, there is no
// agent registry, or the harness's agent does not implement T. A missing
// session is ErrNotFound.
func terminalCapability[T any](ctx context.Context, m *Manager, id domain.SessionID) (rec domain.SessionRecord, capability T, ok bool, err error) {
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return rec, capability, false, err
	}
	if !found {
		return rec, capability, false, ErrNotFound
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat || m.agents == nil {
		return rec, capability, false, nil
	}
	agent, hasAgent := m.agents.Agent(rec.Harness)
	if !hasAgent {
		return rec, capability, false, nil
	}
	capability, ok = agent.(T)
	return rec, capability, ok, nil
}

// readTerminalApproval captures the pane and parses a live dialog. ok is false
// when the session is not a terminal-mode session of a harness that can be
// read this way, or no dialog is on screen.
func (m *Manager) readTerminalApproval(ctx context.Context, id domain.SessionID) (ports.RuntimeHandle, ports.TerminalApproval, bool, error) {
	rec, reader, supported, err := terminalCapability[ports.TerminalApprovalReader](ctx, m, id)
	if err != nil || !supported {
		return ports.RuntimeHandle{}, ports.TerminalApproval{}, false, err
	}
	return m.captureTerminalApproval(ctx, rec, reader)
}

// captureTerminalApproval reads the pane of a session already known to support
// terminal approvals.
func (m *Manager) captureTerminalApproval(ctx context.Context, rec domain.SessionRecord, reader ports.TerminalApprovalReader) (ports.RuntimeHandle, ports.TerminalApproval, bool, error) {
	if rec.IsTerminated || rec.Metadata.RuntimeHandleID == "" {
		return ports.RuntimeHandle{}, ports.TerminalApproval{}, false, nil
	}
	handle := ports.RuntimeHandle{ID: rec.Metadata.RuntimeHandleID}
	output, err := m.runtime.GetOutput(ctx, handle, terminalApprovalLines)
	if err != nil {
		return handle, ports.TerminalApproval{}, false, err
	}
	approval, live := reader.ParseTerminalApproval(output)
	return handle, approval, live, nil
}

// PendingTerminalApprovals lists the permission dialog on a terminal-mode
// session's screen. supported is false for a session this path cannot read.
func (m *Manager) PendingTerminalApprovals(ctx context.Context, id domain.SessionID) (approvals []ports.PendingTerminalApproval, supported bool, err error) {
	rec, reader, supported, err := terminalCapability[ports.TerminalApprovalReader](ctx, m, id)
	if err != nil || !supported {
		return nil, false, err
	}
	_, approval, live, err := m.captureTerminalApproval(ctx, rec, reader)
	if err != nil || !live {
		return nil, true, err
	}
	return []ports.PendingTerminalApproval{{RequestID: terminalApprovalID(approval), Approval: approval}}, true, nil
}

// ResolveTerminalApproval answers the dialog on screen by moving the cursor to
// the chosen option with arrow keys, confirming the cursor landed there, and
// pressing Enter. It refuses when the dialog changed since the caller read it.
func (m *Manager) ResolveTerminalApproval(ctx context.Context, id domain.SessionID, requestID, decisionID string) error {
	// One answer at a time per session: a key sent by another resolve between
	// the confirm read and Enter would move the cursor onto a different row.
	defer terminalApprovalLocks.lock(id)()
	handle, approval, live, err := m.readTerminalApproval(ctx, id)
	if err != nil {
		return err
	}
	if !live || terminalApprovalID(approval) != requestID {
		return ErrTerminalApprovalNotPending
	}
	sender, ok := m.runtime.(runtimeInputSender)
	if !ok {
		return ErrSemanticAcceptanceUnsupported
	}
	target, current := -1, 0
	for _, o := range approval.Options {
		if o.ID == decisionID {
			target = o.Index
		}
		if o.Selected {
			current = o.Index
		}
	}
	if target < 0 {
		return fmt.Errorf("%w: no decision %q", ports.ErrTerminalApprovalNotPending, decisionID)
	}
	key, steps := terminalKeyDown, target-current
	if steps < 0 {
		key, steps = terminalKeyUp, -steps
	}
	for i := 0; i < steps; i++ {
		if err := sender.SendInput(ctx, handle, key); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(terminalKeyPause):
		}
	}
	// Confirm the cursor is on the chosen row before pressing Enter: a wrong
	// row would answer the dialog the opposite way.
	_, after, live, err := m.readTerminalApproval(ctx, id)
	if err != nil {
		return err
	}
	if !live || terminalApprovalID(after) != requestID {
		return ErrTerminalApprovalNotPending
	}
	landed := false
	for _, o := range after.Options {
		if o.Selected {
			landed = o.ID == decisionID
		}
	}
	if !landed {
		return fmt.Errorf("%w: cursor did not reach decision %q", ports.ErrTerminalApprovalNotPending, decisionID)
	}
	return sender.SendInput(ctx, handle, terminalKeyEnter)
}
