// Package accountadopt switches every live session of a provider to one agent
// account ("Use for all sessions") by queueing the per-session account move.
package accountadopt

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Accounts is what the batch needs from the agent accounts.
type Accounts interface {
	// Account resolves an account by id.
	Account(ctx context.Context, id string) (domain.AgentAccount, error)
	// ValidateAccount checks the account is signed in and its folder shares the
	// computer's history (a split-history folder is refused).
	ValidateAccount(ctx context.Context, harness domain.AgentHarness, accountID string) error
	SetDefault(ctx context.Context, accountID string) error
	// SessionAccount is the account a session uses and whether it is bound.
	SessionAccount(ctx context.Context, id domain.SessionID, p domain.AgentAccountProvider) (domain.AgentAccount, bool, error)
	// CommitSession rebinds a session whose agent is not running.
	CommitSession(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, accountID string) error
}

// Sessions lists sessions.
type Sessions interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Mover starts one session's account move and reports its progress.
type Mover interface {
	StartAccountTransition(ctx context.Context, id domain.SessionID, accountID string, policy domain.SessionInterfaceTransitionPolicy) (domain.SessionInterfaceTransition, error)
	GetSessionInterfaceTransition(ctx context.Context, id string) (domain.SessionInterfaceTransition, bool, error)
	// GetActiveSessionInterfaceTransition is the move that owns the session now, if any.
	GetActiveSessionInterfaceTransition(ctx context.Context, id domain.SessionID) (domain.SessionInterfaceTransition, bool, error)
}
