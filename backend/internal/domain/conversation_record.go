package domain

import (
	"time"
)

// ConversationRecord is the durable head of one narrative.
type ConversationRecord struct {
	ID    string            `json:"id"`
	Scope ConversationScope `json:"scope"`
	// ProjectID is set for both scopes. SessionID is the session currently
	// controlling the conversation; for project-scoped orchestrators it changes
	// on clean replacement while the conversation identity remains stable.
	ProjectID ProjectID `json:"projectId"`
	SessionID SessionID `json:"sessionId,omitempty"`
	ReviewID  string    `json:"reviewId,omitempty"`
	// ActiveBranchID identifies the one provider-thread lineage the session may
	// write. Sibling branches remain durable and are selected by moving this head;
	// display status is still derived independently at read time.
	ActiveBranchID string `json:"activeBranchId,omitempty"`
	// LatestSequence is the highest sequence handed out in this conversation.
	// New messages and activities take LatestSequence+1 under the same
	// transaction that bumps it, so ordering has a single writer.
	LatestSequence int64 `json:"latestSequence"`
	// Settings are the provider choices for the conversation's NEXT turn. Empty
	// fields mean the provider's own default, which is why a conversation nobody
	// configures behaves exactly as it did before these existed.
	Settings ConversationSettings `json:"settings"`
	// Usage is how full this conversation is. Nil until the provider reports.
	Usage *ConversationUsage `json:"usage,omitempty"`
	// RateLimits is where the account stands. Nil until the provider reports.
	RateLimits *ConversationRateLimits `json:"rateLimits,omitempty"`
	// CompactedAt is when the provider last summarized earlier history to reclaim
	// context. Nil means never. Held as conversation state as well as a timeline
	// entry so a client can tell whether compaction has run without walking an
	// unbounded timeline on every render.
	CompactedAt *time.Time `json:"compactedAt,omitempty"`
	// ProviderTitle is what the provider currently calls this thread. Kept even
	// when the user has overridden the Tallpine label, because it is the name the
	// conversation carries in the provider's own history.
	ProviderTitle string `json:"providerTitle,omitempty"`
	// ModelReroute records the provider swapping the model mid-conversation. Nil
	// means it never has. Held as conversation state because the alternative is a
	// UI that keeps naming the model the user picked while a different one is
	// answering, which is the one thing a model selector must never do.
	ModelReroute *ConversationModelReroute `json:"modelReroute,omitempty"`
	// Account is the provider account this conversation runs under. Nil until the
	// provider says anything about it.
	Account *ConversationAccount `json:"account,omitempty"`
	// ThreadState is the provider's own view of the thread's lifecycle: whether it
	// is working, archived, or closed. Nil until the provider reports.
	ThreadState *ConversationThreadState `json:"threadState,omitempty"`
	// MCPServers is the startup state of the tool servers this conversation can
	// reach. Empty means none are configured or none has reported yet. It is here
	// because it answers a question the timeline cannot: a tool call that failed
	// because its server never started is not the agent's mistake.
	MCPServers []ConversationMCPServer `json:"mcpServers,omitempty"`
	// AppliedTitle is the last provider title Tallpine wrote into the session's display
	// name. It is what makes "replace a label Tallpine chose" distinguishable from
	// "overwrite a label a person chose". Empty means Tallpine has never named it.
	AppliedTitle string    `json:"-"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// ConversationBranchStrategy records how a provider branch was materialized.
// Native branches preserve provider-owned history; approximate branches seed a
// fresh provider session with bounded Tallpine-owned textual context.
type ConversationBranchStrategy string

const (
	// ConversationBranchStrategyNative preserves the provider's exact history.
	ConversationBranchStrategyNative ConversationBranchStrategy = "native"
	// ConversationBranchStrategyApproximateContext starts a fresh provider
	// session from bounded Tallpine-owned context.
	ConversationBranchStrategyApproximateContext ConversationBranchStrategy = "approximate_context"
)

// NormalizeConversationBranchStrategy keeps pre-strategy rows compatible. Only
// the empty legacy value means native; unknown nonempty values are preserved so
// a newer materialization strategy is never misreported as exact history.
func NormalizeConversationBranchStrategy(strategy ConversationBranchStrategy) ConversationBranchStrategy {
	if strategy == "" {
		return ConversationBranchStrategyNative
	}
	return strategy
}

// ConversationBranch is one durable provider-thread lineage node.
type ConversationBranch struct {
	ID                     string    `json:"id"`
	ConversationID         string    `json:"conversationId"`
	SessionID              SessionID `json:"sessionId"`
	ReviewID               string    `json:"reviewId,omitempty"`
	ProviderConversationID string    `json:"-"`
	ParentBranchID         string    `json:"parentBranchId,omitempty"`
	ForkAfterTurnID        string    `json:"forkAfterTurnId,omitempty"`
	ReplacedTurnID         string    `json:"replacedTurnId,omitempty"`
	ReplacementTurnID      string    `json:"replacementTurnId,omitempty"`
	ForkAfterSequence      int64     `json:"-"`
	// Strategy distinguishes native anchored forks from approximate user-context
	// branches.
	Strategy             ConversationBranchStrategy `json:"strategy,omitempty"`
	ReplayCutoffSequence int64                      `json:"-"`
	ReplayTruncated      bool                       `json:"replayTruncated,omitempty"`
	// ProviderBindingID identifies the durable adapter/configuration epoch that
	// can reopen this branch. It is deliberately separate from ProviderScopeID:
	// approximate siblings own different opaque-id namespaces while remaining
	// reopenable by the same provider binding.
	ProviderBindingID string    `json:"-"`
	ProviderScopeID   string    `json:"-"`
	ProviderIDsScoped bool      `json:"-"`
	Active            bool      `json:"active"`
	CreatedAt         time.Time `json:"createdAt"`

	// HandoffNote is the note a provider switch gave this branch's provider.
	// It is re-applied when that provider restarts and never reaches a client.
	HandoffNote string `json:"-"`
}

// ConversationBranchPoint describes the sibling continuations available at one
// human prompt. Position is one-based; previous and next deliberately do not
// wrap so a client can disable the corresponding edge control.
type ConversationBranchPoint struct {
	TurnID           string `json:"turnId"`
	Position         int    `json:"position"`
	Total            int    `json:"total"`
	PreviousBranchID string `json:"previousBranchId,omitempty"`
	NextBranchID     string `json:"nextBranchId,omitempty"`
}

// ConversationEditAnchor is the immutable source material needed to fork
// immediately before one human prompt. The sequence cutoff applies uniformly to
// every branch-owned timeline table.
type ConversationEditAnchor struct {
	ConversationID              string
	SourceBranchID              string
	ReplacedTurnID              string
	PreviousProviderTurnID      string
	ForkAfterSequence           int64
	ReplayFloorSequence         int64
	HasPriorContext             bool
	OriginalDeliveryContentJSON string
	RetryActiveBranch           bool
}
