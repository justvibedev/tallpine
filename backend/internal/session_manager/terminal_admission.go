package sessionmanager

import (
	"context"
	"fmt"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

const (
	terminalAdmissionCaptureLines = 200
	terminalAdmissionWitnessWait  = 2 * time.Second
	terminalAdmissionPoll         = 20 * time.Millisecond
)

// TerminalSessions is the session read the admission needs.
type TerminalSessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// TerminalMessageWriter writes a message into a runtime pane. A writer that
// also implements ports.TerminalScreenReader enables admission checks.
type TerminalMessageWriter interface {
	SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error
}

// admissionEntry is the per-session admission state. refs counts goroutines
// that hold or wait for it, so the entry can be dropped once nobody does and
// no submission is left unconfirmed.
type admissionEntry struct {
	sync.Mutex
	refs     int
	pending  string
	baseline string
	handleID string
	launchID string
}

// TerminalAdmission sends a message to a session's live terminal. For agents
// with a terminal message policy it serializes sends per session, validates the
// message against the current screen, and holds further input until the agent
// witnesses the previous submission.
type TerminalAdmission struct {
	sessions TerminalSessions
	runtime  TerminalMessageWriter
	agents   ports.AgentResolver

	// witnessWait bounds how long Send waits to see the agent accept input.
	witnessWait time.Duration
	// releaseHook runs between unlocking an entry and dropping its reference
	// (tests force interleavings there).
	releaseHook func()

	mu      sync.Mutex
	entries map[domain.SessionID]*admissionEntry
}

// NewTerminalAdmission builds the admission over a session reader, a runtime
// and (optionally) the agent resolver that supplies per-agent policy.
func NewTerminalAdmission(sessions TerminalSessions, runtime TerminalMessageWriter, agents ports.AgentResolver) *TerminalAdmission {
	return &TerminalAdmission{sessions: sessions, runtime: runtime, agents: agents, witnessWait: terminalAdmissionWitnessWait, entries: map[domain.SessionID]*admissionEntry{}}
}

// tracked is the number of sessions with live admission state (for tests).
func (a *TerminalAdmission) tracked() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.entries)
}

func (a *TerminalAdmission) acquire(id domain.SessionID) *admissionEntry {
	a.mu.Lock()
	e := a.entries[id]
	if e == nil {
		e = &admissionEntry{}
		a.entries[id] = e
	}
	e.refs++
	a.mu.Unlock()
	e.Lock()
	return e
}

// release unlocks e and forgets it when no one else uses it and nothing is
// pending: a fresh entry is indistinguishable from an idle one. pending is read
// only after the decrement, under a.mu: refs == 0 means no goroutine holds or
// can acquire e, so the value is final (every writer ran under e's lock while
// holding a reference).
func (a *TerminalAdmission) release(id domain.SessionID, e *admissionEntry) {
	e.Unlock()
	if a.releaseHook != nil {
		a.releaseHook()
	}
	a.mu.Lock()
	e.refs--
	if e.refs == 0 && e.pending == "" {
		delete(a.entries, id)
	}
	a.mu.Unlock()
}

// Send implements ports.AgentMessenger for terminal sessions.
func (a *TerminalAdmission) Send(ctx context.Context, id domain.SessionID, message string) error {
	rec, ok, err := a.sessions.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session %s: %w", id, ErrNotFound)
	}
	if rec.IsTerminated {
		return fmt.Errorf("session %s: %w", id, ErrTerminated)
	}
	if a.agents != nil {
		if agent, found := a.agents.Agent(rec.Harness); found {
			if policy, supports := agent.(ports.TerminalMessagePolicy); supports {
				return a.sendAdmitted(ctx, id, rec, agent, policy, message)
			}
		}
	}
	handleID := rec.Metadata.RuntimeHandleID
	if handleID == "" {
		return fmt.Errorf("session %s: %w", id, ErrIncompleteHandle)
	}
	return a.runtime.SendMessage(ctx, ports.RuntimeHandle{ID: handleID}, message)
}

func (a *TerminalAdmission) sendAdmitted(ctx context.Context, id domain.SessionID, rec domain.SessionRecord, agent ports.Agent, policy ports.TerminalMessagePolicy, message string) error {
	e := a.acquire(id)
	defer a.release(id, e)
	reader, supports := a.runtime.(ports.TerminalScreenReader)
	if !supports {
		return fmt.Errorf("session %s: current rendered terminal unavailable", id)
	}
	handle := ports.RuntimeHandle{ID: rec.Metadata.RuntimeHandleID}
	screen, err := reader.GetStyledOutput(ctx, handle, terminalAdmissionCaptureLines)
	if err != nil {
		return err
	}
	witness, hasWitness := agent.(ports.TerminalMessageWitness)
	if e.pending != "" && (e.handleID != rec.Metadata.RuntimeHandleID || e.launchID != rec.Metadata.RuntimeLaunchID) {
		e.pending = ""
	}
	if e.pending != "" {
		if !hasWitness || !witness.TerminalMessageObserved(e.pending, e.baseline, screen) {
			// Admission resets only on a new runtime handle or launch, so name the
			// recovery instead of looking like a transient failure.
			return fmt.Errorf("session %s: prior terminal submission remains unconfirmed; check the terminal, then kill and restore the session to resume Tallpine input", id)
		}
		e.pending = ""
	}
	if err := policy.ValidateTerminalMessage(message, screen); err != nil {
		return err
	}
	current, exists, err := a.sessions.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !exists || current.IsTerminated || current.Metadata.RuntimeHandleID != rec.Metadata.RuntimeHandleID || current.Metadata.RuntimeLaunchID != rec.Metadata.RuntimeLaunchID {
		return fmt.Errorf("session %s: runtime changed before input", id)
	}
	e.pending = message
	e.baseline = screen
	e.handleID = rec.Metadata.RuntimeHandleID
	e.launchID = rec.Metadata.RuntimeLaunchID
	if err := a.runtime.SendMessage(ctx, handle, message); err != nil {
		// A runtime may report an error after applying input, so a failed write
		// stays pending until a native witness or a new runtime generation.
		return fmt.Errorf("session %s: terminal submission may have been written: %w", id, err)
	}
	// A socket write acknowledgement precedes native acceptance. Hold admission
	// until the adapter witnesses submission through busy chrome or a committed
	// transcript echo; an unrelated idle redraw cannot release admission.
	deadline := time.NewTimer(a.witnessWait)
	defer deadline.Stop()
	ticker := time.NewTicker(terminalAdmissionPoll)
	defer ticker.Stop()
	for {
		frame, captureErr := reader.GetStyledOutput(ctx, handle, terminalAdmissionCaptureLines)
		if captureErr == nil && hasWitness && witness.TerminalMessageObserved(message, e.baseline, frame) {
			e.pending = ""
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("session %s: terminal submission written but unconfirmed: %w", id, ctx.Err())
		case <-deadline.C:
			return fmt.Errorf("session %s: terminal submission written but native echo unconfirmed; do not automatically retry", id)
		case <-ticker.C:
		}
	}
}
