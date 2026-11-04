package ports

import (
	"context"
	"errors"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ErrAccountAdoptionExists is returned when a batch with the same account and
// request key is already stored.
var ErrAccountAdoptionExists = errors.New("account adoption already exists")

// AccountAdoptionStore persists bulk account switches and their per-session
// outcomes.
type AccountAdoptionStore interface {
	// CreateAccountAdoption stores the batch and its items together; it returns
	// ErrAccountAdoptionExists when (account, request key) is already stored.
	CreateAccountAdoption(ctx context.Context, batch domain.AccountAdoption) error
	GetAccountAdoption(ctx context.Context, id string) (domain.AccountAdoption, bool, error)
	GetAccountAdoptionByKey(ctx context.Context, accountID, requestKey string) (domain.AccountAdoption, bool, error)
	GetLatestAccountAdoption(ctx context.Context, accountID string) (domain.AccountAdoption, bool, error)
	// GetRunningAccountAdoption is the unfinished batch of a provider, if any.
	GetRunningAccountAdoption(ctx context.Context, provider domain.AgentAccountProvider) (domain.AccountAdoption, bool, error)
	ListRunningAccountAdoptions(ctx context.Context) ([]domain.AccountAdoption, error)
	UpdateAccountAdoptionItem(ctx context.Context, batchID string, item domain.AccountAdoptionItem) error
	CompleteAccountAdoption(ctx context.Context, id string) error
}
