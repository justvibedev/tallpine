package ports

import "context"

// TerminalMessagePolicy is implemented by an agent that can refuse a message
// typed into its terminal (for example while a prompt or dialog is open). The
// screen is the current styled capture of the terminal.
type TerminalMessagePolicy interface {
	ValidateTerminalMessage(message, screen string) error
}

// TerminalMessageWitness is implemented by an agent that can tell, from a later
// capture, that a message written to its terminal was actually accepted.
type TerminalMessageWitness interface {
	TerminalMessageObserved(message, baseline, screen string) bool
}

// TerminalScreenReader reads the current rendered terminal of a runtime handle.
type TerminalScreenReader interface {
	GetStyledOutput(ctx context.Context, handle RuntimeHandle, lines int) (string, error)
}
