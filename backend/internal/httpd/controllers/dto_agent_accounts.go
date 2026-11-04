package controllers

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/agentaccounts"
)

// AgentAccountIDParam documents an agent account route identifier.
type AgentAccountIDParam struct {
	AccountID string `path:"accountId" description:"Agent account identifier; device-claude and device-codex are this computer's own logins."`
}

// AgentAccountLoginIDParam documents a sign-in operation route identifier.
type AgentAccountLoginIDParam struct {
	OperationID string `path:"operationId" description:"Agent account sign-in operation identifier."`
}

// AgentAccountSessionIDParam documents the session whose account is read or moved.
type AgentAccountSessionIDParam struct {
	SessionID string `path:"sessionId" description:"Tallpine session identifier."`
}

// AgentAccountListQuery documents GET /agents/accounts.
type AgentAccountListQuery struct {
	Refresh bool `query:"refresh" description:"Read identity and limits again even when the last reading is fresh."`
}

// AgentAccountUsageQuery documents GET /agents/accounts/usage.
type AgentAccountUsageQuery struct {
	Days int `query:"days" minimum:"1" maximum:"90" description:"Days of usage to sum, today included. Defaults to 30."`
}

// AgentAccountResponse is one account with its latest readings.
type AgentAccountResponse struct {
	ID       string                      `json:"id"`
	Provider domain.AgentAccountProvider `json:"provider" enum:"claude,codex"`
	// Device marks the computer's own login, which Tallpine launches with the CLI's
	// config folder untouched.
	Device bool   `json:"device"`
	Email  string `json:"email,omitempty"`
	Plan   string `json:"plan,omitempty"`
	// OrgName names the provider organization of the login, when the CLI
	// reports one; it tells apart two logins that share an email.
	OrgName string `json:"orgName,omitempty"`
	// OrgID is the organization id, shown as a short suffix when no name exists.
	OrgID string `json:"orgId,omitempty"`
	// Label is the owner's own name for the entry.
	Label string `json:"label,omitempty"`
	// DuplicateOf is the id of an earlier entry holding this same login. Both
	// read the same limits, so the entry is redundant.
	DuplicateOf string     `json:"duplicateOf,omitempty"`
	Default     bool       `json:"default"`
	SignedIn    bool       `json:"signedIn"`
	CreatedAt   *time.Time `json:"createdAt,omitempty"`
	// Problem says why identity or limits could not be read.
	Problem string                     `json:"problem,omitempty"`
	Limits  *domain.AgentAccountLimits `json:"limits,omitempty"`
	// HistorySplit names the shared history folders the account keeps its own
	// copy of, so conversations on other accounts cannot resume there. Empty
	// when the account shares them all.
	HistorySplit []string `json:"historySplit"`
}

// SetAgentAccountLabelRequest names a saved account; an empty label clears it.
type SetAgentAccountLabelRequest struct {
	Label string `json:"label" maxLength:"80"`
}

// AgentAccountsResponse is the body of GET /agents/accounts.
type AgentAccountsResponse struct {
	Accounts []AgentAccountResponse `json:"accounts"`
}

// StartAgentAccountLoginRequest asks to add an account of a provider.
type StartAgentAccountLoginRequest struct {
	Provider domain.AgentAccountProvider `json:"provider" enum:"claude,codex"`
}

// StartAgentAccountLoginResponse is a sign-in running in a terminal.
type StartAgentAccountLoginResponse struct {
	OperationID   string                            `json:"operationId"`
	Provider      domain.AgentAccountProvider       `json:"provider" enum:"claude,codex"`
	ShellTerminal CodexAccountLoginTerminalResponse `json:"shellTerminal"`
}

// VerifyAgentAccountLoginResponse is how a sign-in stands. A duplicate login
// is refused and its account names the existing one.
type VerifyAgentAccountLoginResponse struct {
	Status  agentaccounts.LoginStatus `json:"status" enum:"waiting,complete,duplicate,mismatch"`
	Account *AgentAccountResponse     `json:"account,omitempty"`
}

// ConsumeAgentAccountResetCreditRequest spends one Codex usage-limit reset.
type ConsumeAgentAccountResetCreditRequest struct {
	IdempotencyKey string `json:"idempotencyKey" minLength:"1" maxLength:"128"`
}

// ConsumeAgentAccountResetCreditResponse is what the provider did.
type ConsumeAgentAccountResetCreditResponse struct {
	Outcome domain.CodexResetCreditOutcome `json:"outcome" enum:"reset,already_redeemed,nothing_to_reset"`
}

// AgentAccountUsageResponse is each account's usage in Tallpine sessions.
type AgentAccountUsageResponse struct {
	Days  int                    `json:"days"`
	Usage []AgentAccountUsageRow `json:"usage"`
}

// AgentAccountUsageRow is one account's totals, per day and per model.
type AgentAccountUsageRow struct {
	AccountID    string                     `json:"accountId"`
	InputTokens  int64                      `json:"inputTokens"`
	OutputTokens int64                      `json:"outputTokens"`
	CostNanos    int64                      `json:"costNanos"`
	Daily        []AgentAccountUsageDay     `json:"daily"`
	Models       []AgentAccountUsageByModel `json:"models"`
}

// AgentAccountUsageDay is one UTC day of an account's usage.
type AgentAccountUsageDay struct {
	Day       string `json:"day"`
	Tokens    int64  `json:"tokens"`
	CostNanos int64  `json:"costNanos"`
}

// AgentAccountUsageByModel is one model's share of an account's usage.
type AgentAccountUsageByModel struct {
	Model     string `json:"model"`
	Tokens    int64  `json:"tokens"`
	CostNanos int64  `json:"costNanos"`
}

// SessionAgentAccountsResponse is the account a session uses per provider.
type SessionAgentAccountsResponse struct {
	Claude string `json:"claude,omitempty"`
	Codex  string `json:"codex,omitempty"`
}

// SwitchSessionAgentAccountRequest moves a session to another account.
type SwitchSessionAgentAccountRequest struct {
	AccountID string `json:"accountId" minLength:"1"`
	// StopTurn cancels a turn in progress, for an agent stuck waiting on the old
	// account's limit. Otherwise the move waits for the turn to finish.
	StopTurn bool `json:"stopTurn,omitempty"`
}
