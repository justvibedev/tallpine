package codex

import (
	"context"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

func (m *codexAccountManager) ensureUsage(ctx context.Context, record codexAccountRecord) error {
	m.mu.Lock()
	state := m.usage[record.Snapshot.ID]
	if state == nil {
		state = &accountUsageState{}
		m.usage[record.Snapshot.ID] = state
	}
	if state.value != nil && m.now().Sub(state.checkedAt) < codexAccountUsageTTL {
		m.mu.Unlock()
		return nil
	}
	if state.call != nil {
		call := state.call
		m.mu.Unlock()
		select {
		case <-call:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	call := make(chan struct{})
	state.call = call
	m.mu.Unlock()
	go m.runUsage(record, state, call)
	select {
	case <-call:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *codexAccountManager) runUsage(record codexAccountRecord, state *accountUsageState, call chan struct{}) {
	defer func() {
		m.mu.Lock()
		if state.call == call {
			state.call = nil
			close(call)
		}
		m.mu.Unlock()
	}()
	select {
	case m.processes <- struct{}{}:
		defer func() { <-m.processes }()
	case <-m.ctx.Done():
		return
	}
	readCtx, cancel := context.WithTimeout(m.ctx, codexAccountAuthTimeout)
	defer cancel()
	account := m.accountContext(record)
	releaseGlobal, err := m.acquireGlobalRead(readCtx, account)
	if err != nil {
		return
	}
	defer releaseGlobal()
	client, err := m.factory.Open(readCtx, account)
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()
	usage, err := client.ReadUsage(readCtx)
	if err != nil {
		return
	}
	value := &domain.CodexAccountUsageSummary{
		LatestDayTokens: usage.LatestDayTokens, LatestDayStartDate: usage.LatestDayStartDate,
		LifetimeTokens: usage.LifetimeTokens, PeakDailyTokens: usage.PeakDailyTokens,
		LongestRunningTurnSeconds: usage.LongestRunningTurnSeconds,
		CurrentStreakDays:         usage.CurrentStreakDays, LongestStreakDays: usage.LongestStreakDays,
		ObservedAt: usage.ObservedAt,
	}
	m.mu.Lock()
	state.value = value
	state.checkedAt = m.now()
	m.mu.Unlock()
	m.publish()
}

func (m *codexAccountManager) consumeResetCredit(ctx context.Context, accountID, idempotencyKey string) error {
	accountID, idempotencyKey = strings.TrimSpace(accountID), strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return apierr.Invalid("IDEMPOTENCY_KEY_REQUIRED", "Idempotency key is required", nil)
	}
	if err := m.catalog.refresh(); err != nil {
		return apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account discovery is unavailable")
	}
	record, ok := m.catalog.record(accountID)
	if !ok {
		return apierr.NotFound("CODEX_ACCOUNT_NOT_FOUND", "Codex account not found")
	}
	if record.Snapshot.Status != domain.CodexAccountStatusValid {
		return apierr.Conflict("CODEX_ACCOUNT_UNAVAILABLE", "This Codex account is unavailable", nil)
	}
	account := m.accountContext(record)
	releaseGlobal, err := m.acquireGlobalRead(ctx, account)
	if err != nil {
		return err
	}
	defer releaseGlobal()
	release, err := m.acquireAccountMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	m.mu.Lock()
	loginActive := m.login != nil && !terminalLoginStatus(m.login.snapshot.Status)
	m.mu.Unlock()
	if loginActive {
		return apierr.Conflict("CODEX_ACCOUNT_LOGIN_IN_PROGRESS", "Finish or close the Codex account login before using a reset", nil)
	}
	if m.factory == nil {
		return apierr.Unavailable("CODEX_RESET_CREDIT_UNAVAILABLE", "Codex usage-limit reset is unavailable")
	}
	capabilities := m.detectCapabilities(ctx)
	switch capabilities.ResetCreditConsume.State {
	case domain.CodexCapabilityUnsupported:
		return apierr.NotImplemented("CODEX_RESET_CREDIT_UNSUPPORTED", "This Codex version does not support usage-limit resets")
	case domain.CodexCapabilityUnknown, "":
		return apierr.Unavailable("CODEX_RESET_CREDIT_UNAVAILABLE", "Codex usage-limit reset support could not be confirmed")
	}
	select {
	case m.processes <- struct{}{}:
		defer func() { <-m.processes }()
	case <-ctx.Done():
		return ctx.Err()
	}
	operationCtx, cancel := context.WithTimeout(ctx, codexResetCreditTimeout)
	defer cancel()
	client, err := m.factory.Open(operationCtx, account)
	if err != nil {
		return apierr.Unavailable("CODEX_RESET_CREDIT_UNAVAILABLE", "Codex usage-limit reset could not be started")
	}
	defer func() { _ = client.Close() }()
	auth, err := client.Read(operationCtx, true)
	if err != nil || auth.Authentication != domain.AgentAuthenticationAuthorized || auth.Method != domain.CodexAuthMethodChatGPT {
		return apierr.Conflict("CODEX_ACCOUNT_AUTH_UNVERIFIED", "Confirm this Codex account before using a reset", nil)
	}
	attemptedAt := m.now()
	before, err := client.ReadCapacity(operationCtx)
	if err != nil {
		return apierr.Unavailable("CODEX_RESET_CREDIT_UNAVAILABLE", "Available Codex usage-limit resets could not be confirmed")
	}
	m.capacity.acceptDirect(accountID, before, attemptedAt)
	if before.ResetCredits == nil || before.ResetCredits.AvailableCount <= 0 {
		return apierr.Conflict("CODEX_RESET_CREDIT_UNAVAILABLE", "No Codex usage-limit reset is currently available", nil)
	}
	outcome, err := client.ConsumeResetCredit(operationCtx, idempotencyKey)
	if err != nil {
		m.capacity.invalidateAfterReset(accountID)
		return apierr.Unavailable("CODEX_RESET_CREDIT_UNAVAILABLE", "Codex could not confirm the usage-limit reset")
	}
	switch outcome {
	case domain.CodexResetCreditNoCredit:
		m.capacity.invalidateAfterReset(accountID)
		return apierr.Conflict("CODEX_RESET_CREDIT_UNAVAILABLE", "No Codex usage-limit reset is currently available", nil)
	case domain.CodexResetCreditNothingToReset:
		return apierr.Conflict("CODEX_USAGE_LIMIT_RESET_NOT_APPLICABLE", "No current Codex usage limit is eligible for reset", nil)
	case domain.CodexResetCreditReset, domain.CodexResetCreditAlreadyRedeemed:
		m.capacity.invalidateAfterReset(accountID)
	default:
		m.capacity.invalidateAfterReset(accountID)
		return apierr.Unavailable("CODEX_RESET_CREDIT_UNAVAILABLE", "Codex returned an unknown usage-limit reset result")
	}
	after, readErr := client.ReadCapacity(operationCtx)
	if readErr == nil {
		m.capacity.acceptDirect(accountID, after, attemptedAt)
	}
	m.publish()
	return nil
}

func (m *codexAccountManager) subscribe(ctx context.Context) <-chan Accounts {
	ch := make(chan Accounts, 1)
	m.mu.Lock()
	m.subscribers[ch] = struct{}{}
	m.mu.Unlock()
	ch <- m.cached()
	go func() { <-ctx.Done(); m.mu.Lock(); delete(m.subscribers, ch); close(ch); m.mu.Unlock() }()
	return ch
}
func (m *codexAccountManager) publish() {
	m.mu.Lock()
	next := m.now().UnixNano()
	if next <= m.snapshotRevision {
		next = m.snapshotRevision + 1
	}
	m.snapshotRevision = next
	m.mu.Unlock()
	snapshot := m.cached()
	m.mu.Lock()
	defer m.mu.Unlock()
	for ch := range m.subscribers {
		select {
		case ch <- snapshot:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- snapshot:
			default:
			}
		}
	}
}
