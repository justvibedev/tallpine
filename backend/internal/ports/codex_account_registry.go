package ports

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// CodexAccountRegistryStore persists the Codex accounts Tallpine itself
// manages (Source "managed") as domain.AgentAccount rows. The credential stays
// in the account's home folder; only the descriptor fields are stored.
type CodexAccountRegistryStore interface {
	ListAgentAccounts(ctx context.Context) ([]domain.AgentAccount, error)
	CreateAgentAccount(ctx context.Context, account domain.AgentAccount) error
	DeleteAgentAccount(ctx context.Context, id string) error
	// UpdateAgentAccountCodexDescriptor rewrites a Codex account's email, auth
	// method, provider account id and verification time; ErrAgentAccountUnknown
	// when no such Codex account is saved.
	UpdateAgentAccountCodexDescriptor(ctx context.Context, id, email, authMethod, providerAccountID string, verifiedAt time.Time) error
}
