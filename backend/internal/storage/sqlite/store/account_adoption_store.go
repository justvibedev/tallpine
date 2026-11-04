package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

var _ ports.AccountAdoptionStore = (*Store)(nil)

// CreateAccountAdoption stores a batch and its items in one transaction. Writes
// are serialized, so looking the (account, request key) pair up first is safe.
func (s *Store) CreateAccountAdoption(ctx context.Context, batch domain.AccountAdoption) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	if _, err := q.GetAccountAdoptionByKey(ctx, gen.GetAccountAdoptionByKeyParams{AccountID: batch.AccountID, RequestKey: batch.RequestKey}); err == nil {
		return ports.ErrAccountAdoptionExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := q.CreateAccountAdoption(ctx, gen.CreateAccountAdoptionParams{
		ID: batch.ID, AccountID: batch.AccountID, Provider: string(batch.Provider), Policy: string(batch.Policy),
		RequestKey: batch.RequestKey, CreatedAt: batch.CreatedAt.UTC(), UpdatedAt: batch.CreatedAt.UTC(),
	}); err != nil {
		return fmt.Errorf("create account adoption: %w", err)
	}
	for _, item := range batch.Items {
		orchestrator := int64(0)
		if item.IsOrchestrator {
			orchestrator = 1
		}
		if err := q.CreateAccountAdoptionItem(ctx, gen.CreateAccountAdoptionItemParams{
			AdoptionID: batch.ID, SessionID: string(item.SessionID), Position: int64(item.Position),
			IsOrchestrator: orchestrator, UpdatedAt: batch.CreatedAt.UTC(),
		}); err != nil {
			return fmt.Errorf("create account adoption item %s: %w", item.SessionID, err)
		}
	}
	return tx.Commit()
}

// GetAccountAdoption returns a batch with its items.
func (s *Store) GetAccountAdoption(ctx context.Context, id string) (domain.AccountAdoption, bool, error) {
	row, err := s.qr.GetAccountAdoption(ctx, id)
	return s.adoptionResult(ctx, row, err)
}

// GetAccountAdoptionByKey returns the batch an account stored under key.
func (s *Store) GetAccountAdoptionByKey(ctx context.Context, accountID, requestKey string) (domain.AccountAdoption, bool, error) {
	row, err := s.qr.GetAccountAdoptionByKey(ctx, gen.GetAccountAdoptionByKeyParams{AccountID: accountID, RequestKey: requestKey})
	return s.adoptionResult(ctx, row, err)
}

// GetLatestAccountAdoption returns the newest batch of an account.
func (s *Store) GetLatestAccountAdoption(ctx context.Context, accountID string) (domain.AccountAdoption, bool, error) {
	row, err := s.qr.GetLatestAccountAdoption(ctx, accountID)
	return s.adoptionResult(ctx, row, err)
}

// GetRunningAccountAdoption returns the unfinished batch of a provider.
func (s *Store) GetRunningAccountAdoption(ctx context.Context, provider domain.AgentAccountProvider) (domain.AccountAdoption, bool, error) {
	row, err := s.qr.GetRunningAccountAdoption(ctx, string(provider))
	return s.adoptionResult(ctx, row, err)
}

// ListRunningAccountAdoptions returns every unfinished batch.
func (s *Store) ListRunningAccountAdoptions(ctx context.Context) ([]domain.AccountAdoption, error) {
	rows, err := s.qr.ListRunningAccountAdoptions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.AccountAdoption, 0, len(rows))
	for _, row := range rows {
		batch, _, err := s.adoptionResult(ctx, row, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, batch)
	}
	return out, nil
}

// UpdateAccountAdoptionItem records one session's outcome.
func (s *Store) UpdateAccountAdoptionItem(ctx context.Context, batchID string, item domain.AccountAdoptionItem) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.UpdateAccountAdoptionItem(ctx, gen.UpdateAccountAdoptionItemParams{
		State: string(item.State), TransitionID: item.TransitionID, ErrorCode: item.ErrorCode,
		ErrorMessage: item.ErrorMessage, UpdatedAt: item.UpdatedAt.UTC(), AdoptionID: batchID, SessionID: string(item.SessionID),
	})
}

// CompleteAccountAdoption marks a batch finished.
func (s *Store) CompleteAccountAdoption(ctx context.Context, id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.CompleteAccountAdoption(ctx, gen.CompleteAccountAdoptionParams{UpdatedAt: time.Now().UTC(), ID: id})
}

func (s *Store) adoptionResult(ctx context.Context, row gen.AccountAdoption, err error) (domain.AccountAdoption, bool, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AccountAdoption{}, false, nil
	}
	if err != nil {
		return domain.AccountAdoption{}, false, err
	}
	items, err := s.qr.ListAccountAdoptionItems(ctx, row.ID)
	if err != nil {
		return domain.AccountAdoption{}, false, err
	}
	batch := domain.AccountAdoption{
		ID: row.ID, AccountID: row.AccountID, Provider: domain.AgentAccountProvider(row.Provider),
		Policy: domain.AccountAdoptionPolicy(row.Policy), RequestKey: row.RequestKey,
		State: domain.AccountAdoptionState(row.State), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		Items: make([]domain.AccountAdoptionItem, 0, len(items)),
	}
	for _, it := range items {
		batch.Items = append(batch.Items, domain.AccountAdoptionItem{
			SessionID: domain.SessionID(it.SessionID), Position: int(it.Position), IsOrchestrator: it.IsOrchestrator == 1,
			State: domain.AccountAdoptionItemState(it.State), TransitionID: it.TransitionID,
			ErrorCode: it.ErrorCode, ErrorMessage: it.ErrorMessage, UpdatedAt: it.UpdatedAt,
		})
	}
	return batch, true, nil
}
