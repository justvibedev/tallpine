package daemon

import (
	"context"
	"log/slog"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/accountadopt"
	"tallpine.eu/tallpine/backend/internal/service/agentaccounts"
	sqlite "tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// accountMover starts a session's account move on the manager and reads its
// durable phase from the store.
type accountMover struct {
	mgr interface {
		StartAccountTransition(context.Context, domain.SessionID, string, domain.SessionInterfaceTransitionPolicy) (domain.SessionInterfaceTransition, error)
	}
	store *sqlite.Store
}

func (m accountMover) StartAccountTransition(ctx context.Context, id domain.SessionID, accountID string, policy domain.SessionInterfaceTransitionPolicy) (domain.SessionInterfaceTransition, error) {
	return m.mgr.StartAccountTransition(ctx, id, accountID, policy)
}

func (m accountMover) GetActiveSessionInterfaceTransition(ctx context.Context, id domain.SessionID) (domain.SessionInterfaceTransition, bool, error) {
	return m.store.GetActiveSessionInterfaceTransition(ctx, id)
}

func (m accountMover) GetSessionInterfaceTransition(ctx context.Context, id string) (domain.SessionInterfaceTransition, bool, error) {
	return m.store.GetSessionInterfaceTransition(ctx, id)
}

// newAccountAdopt builds "Use for all sessions", or nil when the session
// manager cannot move a session to another account.
func newAccountAdopt(ctx context.Context, mgr any, store *sqlite.Store, accounts *agentaccounts.Service, log *slog.Logger) *accountadopt.Service {
	starter, ok := mgr.(interface {
		StartAccountTransition(context.Context, domain.SessionID, string, domain.SessionInterfaceTransitionPolicy) (domain.SessionInterfaceTransition, error)
	})
	if !ok || accounts == nil {
		return nil
	}
	svc := accountadopt.New(accountadopt.Config{
		Store: store, Accounts: accounts, Sessions: store, Mover: accountMover{mgr: starter, store: store}, Log: log,
	})
	if err := svc.Start(ctx); err != nil {
		log.Warn("account adoption: resume running batches", "error", err)
	}
	return svc
}
