package domain

import (
	"strings"
	"time"
)

// AgentAccountProvider names a provider whose subscription accounts Tallpine can
// launch agents with. An account is a config folder of the provider's own CLI.
type AgentAccountProvider string

// Account providers and the harness each one launches.
const (
	AgentAccountProviderClaude AgentAccountProvider = "claude"
	AgentAccountProviderCodex  AgentAccountProvider = "codex"
)

// AgentAccountProviders lists every provider in display order.
var AgentAccountProviders = []AgentAccountProvider{AgentAccountProviderClaude, AgentAccountProviderCodex}

// AgentAccountProviderFor reports the account provider behind a harness, or
// false when the harness has no accounts.
func AgentAccountProviderFor(h AgentHarness) (AgentAccountProvider, bool) {
	switch h {
	case HarnessClaudeCode:
		return AgentAccountProviderClaude, true
	case HarnessCodex:
		return AgentAccountProviderCodex, true
	}
	return "", false
}

// DeviceAgentAccountID is the id of a provider's "This computer" account: the
// login the computer's own CLI uses, launched with its config folder untouched.
func DeviceAgentAccountID(p AgentAccountProvider) string { return "device-" + string(p) }

// AgentAccount is one subscription login Tallpine can launch a provider's CLI with.
// Home is the CLI config folder (CLAUDE_CONFIG_DIR or CODEX_HOME); it is empty
// for the device account, whose folder is the CLI's own default.
type AgentAccount struct {
	ID       string
	Provider AgentAccountProvider
	Home     string
	Email    string
	Plan     string
	// OrgID and OrgName are the provider organization the login belongs to, when
	// the CLI reports one. A personal and a team login can share an email.
	OrgID   string
	OrgName string
	// Label is the owner's own name for the entry; empty when unset.
	Label     string
	CreatedAt time.Time
	// Codex registry descriptor, kept for accounts the file registry created
	// (empty otherwise): how the account was added, how it signs in, the
	// provider's own account id, and when the provider last confirmed it.
	Source            string
	AuthMethod        string
	ProviderAccountID string
	VerifiedAt        *time.Time
}

// Managed reports an account the Codex account registry owns (Source
// "managed"): login, switching and capacity run through the Codex account
// service, so the generic account list does not show it.
func (a AgentAccount) Managed() bool { return a.Source == string(CodexAccountSourceManaged) }

// Device reports the "This computer" account.
func (a AgentAccount) Device() bool { return strings.TrimSpace(a.Home) == "" }

// AgentAccountLimitWindow is one usage meter of an account, such as the
// five-hour session or the weekly limit.
type AgentAccountLimitWindow struct {
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	UsedPercent float64    `json:"usedPercent" minimum:"0" maximum:"100"`
	ResetsAt    *time.Time `json:"resetsAt,omitempty"`
}

// AgentAccountLimits is what the provider's CLI reports about an account's
// subscription limits. Tallpine never reads the account's token to get it.
type AgentAccountLimits struct {
	Windows []AgentAccountLimitWindow `json:"windows"`
	// AutoResumeResetsAt is present only when native rolling limits identify an
	// unambiguous future deadline. Nil does not establish available capacity.
	AutoResumeResetsAt *time.Time `json:"-"`
	// ExtraUsage is Claude's paid usage beyond the plan, in the plan's currency.
	ExtraUsage *AgentAccountExtraUsage `json:"extraUsage,omitempty"`
	// ResetCredits counts Codex usage-limit resets the account can spend.
	ResetCredits *int64 `json:"resetCredits,omitempty" minimum:"0"`
	// Activity is the provider's own count of the account's use everywhere,
	// not only in Tallpine. Codex reports it; Claude does not.
	Activity  *AgentAccountActivity `json:"activity,omitempty"`
	FetchedAt time.Time             `json:"fetchedAt"`
}

// Headroom is how far the account is from its fullest usage window, in percent
// of that window left. known is false when no window was read, so nothing is
// established about capacity.
func (l AgentAccountLimits) Headroom() (headroom float64, known bool) {
	headroom = 100
	for _, w := range l.Windows {
		known = true
		if left := 100 - w.UsedPercent; left < headroom {
			headroom = left
		}
	}
	return headroom, known
}

// AgentAccountActivity is the provider's own record of an account's use.
type AgentAccountActivity struct {
	LatestDayTokens    *int64  `json:"latestDayTokens,omitempty"`
	LatestDay          *string `json:"latestDay,omitempty"`
	LifetimeTokens     *int64  `json:"lifetimeTokens,omitempty"`
	PeakDailyTokens    *int64  `json:"peakDailyTokens,omitempty"`
	LongestTurnSeconds *int64  `json:"longestTurnSeconds,omitempty"`
	CurrentStreakDays  *int64  `json:"currentStreakDays,omitempty"`
}

// AgentAccountExtraUsage is spend beyond the plan's included usage, in major
// units of Currency.
type AgentAccountExtraUsage struct {
	Enabled      bool     `json:"enabled"`
	UsedCredits  *float64 `json:"usedCredits,omitempty"`
	MonthlyLimit *float64 `json:"monthlyLimit,omitempty"`
	Currency     string   `json:"currency,omitempty"`
}

// AgentAccountUsage is one account's model usage on one UTC day, from Tallpine's own
// reading of the session logs.
type AgentAccountUsage struct {
	AccountID         string
	Day               string
	Model             string
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	CostNanos         int64
}

// AgentAccountIdentityCheck is who a config folder is signed in to, as the
// provider's CLI reports it.
type AgentAccountIdentityCheck struct {
	// SignedIn is a subscription sign-in, the only kind an account can be.
	SignedIn bool
	// LoggedIn is any sign-in the CLI can launch with, an API key included.
	LoggedIn bool
	Email    string
	Plan     string
	// OrgID and OrgName identify the organization of the login, when reported.
	OrgID   string
	OrgName string
}

// SameLogin reports whether two sign-ins are one underlying account: the same
// email, and the same organization when both sides report one.
func (c AgentAccountIdentityCheck) SameLogin(email, orgID string) bool {
	return SameAgentLogin(c.Email, c.OrgID, email, orgID)
}

// SameAgentLogin compares two (email, organization) pairs. Emails match
// case-insensitively; organizations are compared only when both are known, so
// an entry saved before organizations were recorded still matches by email.
func SameAgentLogin(emailA, orgA, emailB, orgB string) bool {
	if emailA == "" || !strings.EqualFold(emailA, emailB) {
		return false
	}
	return orgA == "" || orgB == "" || orgA == orgB
}
