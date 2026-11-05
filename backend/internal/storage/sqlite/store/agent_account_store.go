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

var _ ports.AgentAccountStore = (*Store)(nil)

// ListAgentAccounts returns the saved accounts; the device accounts are implicit.
func (s *Store) ListAgentAccounts(ctx context.Context) ([]domain.AgentAccount, error) {
	rows, err := s.qr.ListAgentAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list agent accounts: %w", err)
	}
	out := make([]domain.AgentAccount, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.AgentAccount{
			ID: row.ID, Provider: domain.AgentAccountProvider(row.Provider), Home: row.Home,
			Email: row.Email, Plan: row.Plan, CreatedAt: row.CreatedAt,
			OrgID: row.OrgID, OrgName: row.OrgName, Label: row.Label,
			Source: row.Source, AuthMethod: row.AuthMethod, ProviderAccountID: row.ProviderAccountID,
			VerifiedAt: nullTimePtr(row.VerifiedAt),
		})
	}
	return out, nil
}

// CreateAgentAccount saves a signed-in account folder.
func (s *Store) CreateAgentAccount(ctx context.Context, a domain.AgentAccount) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.CreateAgentAccount(ctx, gen.CreateAgentAccountParams{
		ID: a.ID, Provider: string(a.Provider), Home: a.Home, Email: a.Email, Plan: a.Plan, CreatedAt: a.CreatedAt.UTC(), OrgID: a.OrgID, OrgName: a.OrgName,
		Source: a.Source, AuthMethod: a.AuthMethod, ProviderAccountID: a.ProviderAccountID, VerifiedAt: ptrTimeToNullTime(a.VerifiedAt),
	}); err != nil {
		return fmt.Errorf("create agent account %s: %w", a.ID, err)
	}
	return nil
}

// UpdateAgentAccountOrg records the organization of a saved account's login.
func (s *Store) UpdateAgentAccountOrg(ctx context.Context, id, orgID, orgName string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpdateAgentAccountOrg(ctx, gen.UpdateAgentAccountOrgParams{OrgID: orgID, OrgName: orgName, ID: id}); err != nil {
		return fmt.Errorf("update agent account %s organization: %w", id, err)
	}
	return nil
}

// SetAgentAccountLabel names a saved account.
func (s *Store) SetAgentAccountLabel(ctx context.Context, id, label string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.SetAgentAccountLabel(ctx, gen.SetAgentAccountLabelParams{Label: label, ID: id})
	if err != nil {
		return fmt.Errorf("label agent account %s: %w", id, err)
	}
	if n == 0 {
		return ports.ErrAgentAccountUnknown
	}
	return nil
}

// DeleteAgentAccount removes an account and any default that named it. Session
// bindings stay for usage history; a session bound to a removed account falls
// back to the provider's default at its next launch.
func (s *Store) DeleteAgentAccount(ctx context.Context, id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "delete agent account", func(q *gen.Queries) error {
		if err := q.DeleteAgentAccountDefaultFor(ctx, id); err != nil {
			return err
		}
		return q.DeleteAgentAccount(ctx, id)
	})
}

// GetAgentAccountDefault returns the account new sessions of the provider use.
func (s *Store) GetAgentAccountDefault(ctx context.Context, p domain.AgentAccountProvider) (string, bool, error) {
	id, err := s.qr.GetAgentAccountDefault(ctx, string(p))
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s account default: %w", p, err)
	}
	return id, true, nil
}

// SetAgentAccountDefault makes accountID the provider's account for new sessions.
func (s *Store) SetAgentAccountDefault(ctx context.Context, p domain.AgentAccountProvider, accountID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetAgentAccountDefault(ctx, gen.SetAgentAccountDefaultParams{Provider: string(p), AccountID: accountID}); err != nil {
		return fmt.Errorf("set %s account default: %w", p, err)
	}
	return nil
}

// BindSessionAgentAccount records the account a session uses from at on.
func (s *Store) BindSessionAgentAccount(ctx context.Context, id domain.SessionID, p domain.AgentAccountProvider, accountID string, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	// A rebind must not disarm a parked usage-limit resume (B476): a switch
	// away and back leaves the turn on its own account, and a real mismatch is
	// refused when the resume is consumed (consumeDueAutoResume).
	return s.qw.BindSessionAgentAccount(ctx, gen.BindSessionAgentAccountParams{
		SessionID: string(id), Provider: string(p), AccountID: accountID, BoundAt: at.UTC(),
	})
}

// GetSessionAgentAccount returns the account the session uses now for the provider.
func (s *Store) GetSessionAgentAccount(ctx context.Context, id domain.SessionID, p domain.AgentAccountProvider) (string, bool, error) {
	accountID, err := s.qr.GetSessionAgentAccount(ctx, gen.GetSessionAgentAccountParams{SessionID: string(id), Provider: string(p)})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read session %s %s account: %w", id, p, err)
	}
	return accountID, true, nil
}

// AgentAccountUsageSince sums model usage per account, UTC day and model,
// including the spend of sessions deleted since (counted from since's UTC
// day). An account, day and model may appear twice (live and deleted);
// callers sum.
func (s *Store) AgentAccountUsageSince(ctx context.Context, since time.Time) ([]domain.AgentAccountUsage, error) {
	since = since.UTC()
	rows, err := s.qr.AgentAccountUsageSince(ctx, gen.AgentAccountUsageSinceParams{Since: sql.NullTime{Time: since, Valid: true}})
	if err != nil {
		return nil, fmt.Errorf("read agent account usage: %w", err)
	}
	deleted, err := s.qr.DeletedSessionAccountUsageSince(ctx, since.Format(time.DateOnly))
	if err != nil {
		return nil, fmt.Errorf("read deleted sessions' agent account usage: %w", err)
	}
	out := make([]domain.AgentAccountUsage, 0, len(rows)+len(deleted))
	for _, row := range deleted {
		out = append(out, domain.AgentAccountUsage{
			AccountID: row.AccountID, Day: row.Day, Model: row.ModelID,
			InputTokens: row.InputTokens, CachedInputTokens: row.CachedInputTokens, OutputTokens: row.OutputTokens,
			CostNanos: row.EstimatedCostNanos,
		})
	}
	for _, row := range rows {
		out = append(out, domain.AgentAccountUsage{
			AccountID: row.AccountID, Day: row.Day, Model: row.ModelID,
			InputTokens: row.InputTokens, CachedInputTokens: row.CachedInputTokens, OutputTokens: row.OutputTokens,
			CostNanos: row.EstimatedCostNanos,
		})
	}
	return out, nil
}

var _ ports.CodexAccountRegistryStore = (*Store)(nil)

// UpdateAgentAccountCodexDescriptor rewrites the Codex descriptor fields of a saved account.
func (s *Store) UpdateAgentAccountCodexDescriptor(ctx context.Context, id, email, authMethod, providerAccountID string, verifiedAt time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.UpdateAgentAccountCodexDescriptor(ctx, gen.UpdateAgentAccountCodexDescriptorParams{
		Email: email, AuthMethod: authMethod, ProviderAccountID: providerAccountID,
		VerifiedAt: ptrTimeToNullTime(&verifiedAt), ID: id,
	})
	if err != nil {
		return fmt.Errorf("update agent account %s codex descriptor: %w", id, err)
	}
	if n == 0 {
		return ports.ErrAgentAccountUnknown
	}
	return nil
}
