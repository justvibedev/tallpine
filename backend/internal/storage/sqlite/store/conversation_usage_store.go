package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

func nullableString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

// RecordUsage stores the conversation's current token position, overwriting the
// previous one.
//
// Latest-wins on purpose. The provider reports usage after every tool call, and
// the history of those reports is not information anyone wants: the question is
// "how full is this conversation now", which has exactly one answer.
func (s *Store) RecordUsage(
	ctx context.Context,
	conversationID string,
	usage domain.ConversationUsage,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.UpdateConversationUsage(ctx, gen.UpdateConversationUsageParams{
		ContextUsed:       nullablePositive(usage.ContextUsed),
		ContextWindow:     nullablePositive(usage.ContextWindow),
		UsageInputTokens:  nullablePositive(usage.InputTokens),
		UsageOutputTokens: nullablePositive(usage.OutputTokens),
		UsageCachedTokens: nullablePositive(usage.CachedTokens),
		UsageTotalTokens:  nullablePositive(usage.TotalTokens),
		UsageCost:         nullableCost(usage.Cost),
		UsageCurrency:     nullableString(usage.Currency),
		ID:                conversationID,
	}); err != nil {
		return fmt.Errorf("record usage for %s: %w", conversationID, err)
	}
	return nil
}

// RecordRateLimits stores the account's current quota position, overwriting the
// previous one.
func (s *Store) RecordRateLimits(
	ctx context.Context,
	conversationID string,
	limits domain.ConversationRateLimits,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.UpdateConversationRateLimits(ctx, gen.UpdateConversationRateLimitsParams{
		RateLimitPrimaryPercent:    nullablePercent(limits.PrimaryUsedPercent),
		RateLimitSecondaryPercent:  nullablePercent(limits.SecondaryUsedPercent),
		RateLimitPrimaryResetsIn:   nullablePositive(limits.PrimaryResetsInSeconds),
		RateLimitSecondaryResetsIn: nullablePositive(limits.SecondaryResetsInSeconds),
		RateLimitPlan:              nullableString(limits.PlanLabel),
		ID:                         conversationID,
	}); err != nil {
		return fmt.Errorf("record rate limits for %s: %w", conversationID, err)
	}
	return nil
}

// nullablePositive stores zero as NULL. For every column that uses it, zero and
// unreported are the same thing to a reader, and NULL is the honest encoding of
// "the provider never said".
func nullablePositive(value int64) sql.NullInt64 {
	if value <= 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: value, Valid: true}
}

// nullablePercent stores an unreported window as NULL. Negative is the port's
// "not reported" signal, and it must not be written as a real percentage: a
// reader seeing -1 as data would draw a meter running backwards.
func nullablePercent(value float64) sql.NullFloat64 {
	if value < 0 {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: value, Valid: true}
}

// RecordModelReroute stores the provider answering with a model other than the one
// asked for, overwriting the previous report.
//
// Latest wins because there is one current answer to "which model is actually
// replying". The earlier reroutes of a long conversation are not lost information a
// reader wants: the turn each one happened on is recorded with it, and what a client
// needs to say is what is answering now.
func (s *Store) RecordModelReroute(
	ctx context.Context,
	conversationID string,
	reroute domain.ConversationModelReroute,
) error {
	encoded, err := json.Marshal(reroute)
	if err != nil {
		return fmt.Errorf("encode model reroute for %s: %w", conversationID, err)
	}
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.UpdateConversationModelReroute(ctx,
		gen.UpdateConversationModelRerouteParams{
			ModelRerouteJson: sql.NullString{String: string(encoded), Valid: true},
			UpdatedAt:        reroute.At,
			ID:               conversationID,
		}); err != nil {
		return fmt.Errorf("record model reroute for %s: %w", conversationID, err)
	}
	return nil
}

// RecordAccount stores what the provider says about the account this conversation
// runs under.
//
// The caller passes the whole account, not a patch: the provider reports auth mode
// and plan together on account/updated and neither on a credential demand, so
// merging is the caller's job and it needs the previous value to do it. Doing it
// here would need a read inside the write lock for a field nothing queries.
func (s *Store) RecordAccount(
	ctx context.Context,
	conversationID string,
	account domain.ConversationAccount,
	now time.Time,
) error {
	encoded, err := json.Marshal(account)
	if err != nil {
		return fmt.Errorf("encode account for %s: %w", conversationID, err)
	}
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.UpdateConversationAccount(ctx, gen.UpdateConversationAccountParams{
		AccountJson: sql.NullString{String: string(encoded), Valid: true},
		UpdatedAt:   now,
		ID:          conversationID,
	}); err != nil {
		return fmt.Errorf("record account for %s: %w", conversationID, err)
	}
	return nil
}

// ConversationAccount reads account facts through the projection transaction when present.
func (s *Store) ConversationAccount(ctx context.Context, conversationID string) (*domain.ConversationAccount, error) {
	row, err := s.conversationReader(ctx).SelectConversationByID(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	return conversationToDomain(row).Account, nil
}

// ReconcileConversationAuthentication clears a demand only from durable provider
// success in the owning generation and active branch. Empty providerTurnID permits
// bounded legacy reconciliation on reconnect/read, never a liveness inference.
// The writer transaction makes evidence selection and demand clearing atomic with
// newer failures, generation claims, and branch/account changes.
func (s *Store) ReconcileConversationAuthentication(ctx context.Context, conversationID, generation, providerTurnID string, now time.Time) (*domain.ConversationAccount, error) {
	if _, ok := ctx.Value(conversationProjectionTxKey{}).(*gen.Queries); !ok {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		tx, err := s.writeDB.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }()
		account, err := s.ReconcileConversationAuthentication(context.WithValue(ctx, conversationProjectionTxKey{}, s.qw.WithTx(tx)), conversationID, generation, providerTurnID, now)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return account, nil
	}
	q := s.conversationReader(ctx)
	row, err := q.SelectConversationByID(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	account := conversationToDomain(row).Account
	if account == nil {
		if providerTurnID == "" {
			return nil, nil
		}
		account = &domain.ConversationAccount{AuthenticationState: "unknown"}
	}
	cutoff := row.CreatedAt
	if account.ReauthRequiredAt != nil {
		cutoff = *account.ReauthRequiredAt
	}
	if account.AuthChangedAt != nil && account.AuthChangedAt.After(cutoff) {
		cutoff = *account.AuthChangedAt
	}
	turn, err := q.SelectVerifiedAuthenticationTurn(ctx, gen.SelectVerifiedAuthenticationTurnParams{
		ConversationID: conversationID, Generation: generation, ProviderTurnID: providerTurnID,
		AfterAt: sql.NullTime{Time: cutoff, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return account, nil
	}
	if err != nil {
		return nil, err
	}
	if account.AuthVerifiedAt != nil && !turn.CompletedAt.Time.After(*account.AuthVerifiedAt) && account.ReauthRequiredAt == nil {
		return account, nil
	}
	markAuthenticated(account, turn.CompletedAt.Time)
	if err := s.RecordAccount(ctx, conversationID, *account, now); err != nil {
		return nil, err
	}
	return account, nil
}

// ConversationAwaitingLogin is a session conversation whose provider asked for
// credentials, and the harness its session runs.
type ConversationAwaitingLogin struct {
	ConversationID string
	SessionID      domain.SessionID
	Harness        domain.AgentHarness
}

// ConversationsAwaitingLogin lists session conversations with an unanswered
// credential demand.
func (s *Store) ConversationsAwaitingLogin(ctx context.Context) ([]ConversationAwaitingLogin, error) {
	rows, err := s.qr.SelectConversationsAwaitingLogin(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ConversationAwaitingLogin, 0, len(rows))
	for _, row := range rows {
		out = append(out, ConversationAwaitingLogin(row))
	}
	return out, nil
}

// ConfirmConversationLogin clears a credential demand that a later check of the
// session's account answered: at is when a check began that found the account
// signed in and read its limits from the provider with that login. A demand
// recorded at or after at stays, since that check cannot speak for it.
func (s *Store) ConfirmConversationLogin(ctx context.Context, conversationID string, at, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	cleared := false
	err := s.inTx(ctx, "confirm conversation login", func(q *gen.Queries) error {
		row, err := q.SelectConversationByID(ctx, conversationID)
		if err != nil {
			return err
		}
		account := conversationToDomain(row).Account
		if account == nil || account.ReauthRequiredAt == nil || !account.ReauthRequiredAt.Before(at) {
			return nil
		}
		markAuthenticated(account, at)
		cleared = true
		return s.RecordAccount(context.WithValue(ctx, conversationProjectionTxKey{}, q), conversationID, *account, now)
	})
	return cleared && err == nil, err
}

// markAuthenticated records provider success at verifiedAt. A demand it
// answers is kept as the last failure.
func markAuthenticated(account *domain.ConversationAccount, verifiedAt time.Time) {
	if account.ReauthRequiredAt != nil && account.LastAuthFailureAt == nil {
		account.LastAuthFailureAt = account.ReauthRequiredAt
		account.LastAuthFailureReason = account.ReauthReason
	}
	account.AuthenticationState = "authenticated"
	account.AuthVerifiedAt = &verifiedAt
	account.ReauthRequiredAt = nil
	account.ReauthReason = ""
}

// RecordThreadState stores the provider's lifecycle view of the thread.
//
// updated_at is deliberately not bumped by the statement this runs: thread status
// flips active/idle on every turn, and marking the conversation modified twice per
// message would make every chat session look freshly active forever.
func (s *Store) RecordThreadState(
	ctx context.Context,
	conversationID string,
	state domain.ConversationThreadState,
) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode thread state for %s: %w", conversationID, err)
	}
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.UpdateConversationThreadState(ctx,
		gen.UpdateConversationThreadStateParams{
			ThreadStateJson: sql.NullString{String: string(encoded), Valid: true},
			ID:              conversationID,
		}); err != nil {
		return fmt.Errorf("record thread state for %s: %w", conversationID, err)
	}
	return nil
}

// RecordMCPServers stores the tool servers' startup state.
//
// The whole list is written, not one server: the provider reports servers one at a
// time and re-reports all of them on every turn, so the caller merges by name and
// this stores the result. An empty list is stored as an empty array rather than
// NULL, because "Tallpine asked and there are none" is a different answer from "Tallpine never
// asked".
func (s *Store) RecordMCPServers(
	ctx context.Context,
	conversationID string,
	servers []domain.ConversationMCPServer,
) error {
	if servers == nil {
		servers = []domain.ConversationMCPServer{}
	}
	encoded, err := json.Marshal(servers)
	if err != nil {
		return fmt.Errorf("encode mcp servers for %s: %w", conversationID, err)
	}
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.UpdateConversationMcpServers(ctx,
		gen.UpdateConversationMcpServersParams{
			McpServersJson: sql.NullString{String: string(encoded), Valid: true},
			ID:             conversationID,
		}); err != nil {
		return fmt.Errorf("record mcp servers for %s: %w", conversationID, err)
	}
	return nil
}

// SetTurnPlan overwrites a turn's plan and reports whether the turn was found.
//
// A plan for a turn Tallpine never recorded happens after a restart, when a controller
// reattaches to a provider turn that predates it. There is nothing to update, and
// that is not a failure.
func (s *Store) SetTurnPlan(
	ctx context.Context,
	conversationID, providerTurnID string,
	plan domain.ConversationPlan,
) (bool, error) {
	if providerTurnID == "" {
		// Without a turn id there is no row to attribute this to, and guessing the
		// most recent turn would file one turn's plan under another.
		return false, nil
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return false, fmt.Errorf("encode plan for %s: %w", providerTurnID, err)
	}

	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	rows, err := q.UpdateConversationTurnPlan(ctx, gen.UpdateConversationTurnPlanParams{
		PlanJson:       string(encoded),
		ConversationID: conversationID,
		ProviderTurnID: providerTurnID,
	})
	if err != nil {
		return false, fmt.Errorf("set turn plan for %s: %w", providerTurnID, err)
	}
	return rows > 0, nil
}
