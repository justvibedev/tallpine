package domain

import (
	"time"
)

// ConversationUsage is the conversation's token position.
//
// Latest-wins state on the conversation, not timeline entries. The provider
// reports this after every tool call, so a row per report is what buried the
// conversation the first time round; there is only ever one current answer to
// "how full is this".
type ConversationUsage struct {
	// ContextUsed and ContextWindow are what make the readout mean anything: a
	// used figure without the window is a number with no scale, which is exactly
	// what the header used to show.
	ContextUsed   int64 `json:"contextUsed"`
	ContextWindow int64 `json:"contextWindow"`
	// The cumulative spend for the conversation, which grows without bound and is
	// deliberately kept apart from the context figures above.
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	CachedTokens int64 `json:"cachedTokens"`
	TotalTokens  int64 `json:"totalTokens"`
	// Cost is cumulative provider-reported spend. Nil is distinct from zero: most
	// subscription sessions report token position but no monetary charge.
	Cost     *float64 `json:"cost,omitempty"`
	Currency string   `json:"currency,omitempty"`
}

// ContextFraction is how full the context is, in 0..1, or -1 when the provider
// did not state a window.
//
// A caller must handle the negative case rather than dividing: without a window
// there is no honest fullness to report, and defaulting to 0 would draw an empty
// meter for a conversation that might be nearly full.
func (u ConversationUsage) ContextFraction() float64 {
	if u.ContextWindow <= 0 || u.ContextUsed < 0 {
		return -1
	}
	return float64(u.ContextUsed) / float64(u.ContextWindow)
}

// ConversationRateLimits is the account's quota position.
//
// Stored because it explains a class of failure the user cannot otherwise
// diagnose: a turn that fails for a reason unrelated to what they asked.
type ConversationRateLimits struct {
	// Percentages in 0..100. Negative means the provider did not report that
	// window, which is not the same as reporting it empty.
	PrimaryUsedPercent       float64 `json:"primaryUsedPercent"`
	SecondaryUsedPercent     float64 `json:"secondaryUsedPercent"`
	PrimaryResetsInSeconds   int64   `json:"primaryResetsInSeconds,omitempty"`
	SecondaryResetsInSeconds int64   `json:"secondaryResetsInSeconds,omitempty"`
	// PlanLabel is the provider's name for the account tier, when it says.
	PlanLabel string `json:"planLabel,omitempty"`
}

// WorstUsedPercent is the tighter of the two windows, or -1 when neither was
// reported. The tighter one is what will actually stop the next turn.
func (l ConversationRateLimits) WorstUsedPercent() float64 {
	worst := l.PrimaryUsedPercent
	if l.SecondaryUsedPercent > worst {
		worst = l.SecondaryUsedPercent
	}
	if worst < 0 {
		return -1
	}
	return worst
}

// ConversationModelReroute records the provider answering with a model other than
// the one that was asked for.
//
// Durable because it is a correction to a claim Tallpine has already made. The composer
// says which model it is sending to; if the provider silently substitutes another,
// every later reading of that turn attributes the answer to the wrong model unless
// the substitution is written down.
type ConversationModelReroute struct {
	FromModel string `json:"fromModel"`
	ToModel   string `json:"toModel"`
	// Reason is the provider's own word for why. Carried verbatim rather than
	// translated: Tallpine cannot improve on the provider's account of its own policy.
	Reason string `json:"reason,omitempty"`
	// ProviderTurnID is the turn the reroute happened on, so a client can point at
	// the exchange rather than only at the conversation.
	ProviderTurnID string    `json:"providerTurnId,omitempty"`
	At             time.Time `json:"at"`
}

// ConversationAccount is the provider account a conversation runs under.
type ConversationAccount struct {
	// AuthenticationState is unknown until the provider supplies failure or success evidence.
	AuthenticationState string     `json:"authenticationState,omitempty"`
	AuthVerifiedAt      *time.Time `json:"authVerifiedAt,omitempty"`
	// Failure evidence survives recovery; current credential demand is separate below.
	LastAuthFailureAt     *time.Time `json:"lastAuthFailureAt,omitempty"`
	LastAuthFailureReason string     `json:"lastAuthFailureReason,omitempty"`
	AuthFailureID         string     `json:"authFailureId,omitempty"`
	// AuthChangedAt fences success from a turn started before an account/auth-mode change.
	AuthChangedAt *time.Time `json:"authChangedAt,omitempty"`
	// AuthMode is the provider's name for how it authenticates (chatgpt, apikey...).
	AuthMode string `json:"authMode,omitempty"`
	// PlanLabel is the account tier the provider reports.
	PlanLabel string `json:"planLabel,omitempty"`
	// ReauthRequiredAt is when the provider last asked for credentials Tallpine does not
	// hold. Nil means there is no current demand.
	ReauthRequiredAt *time.Time `json:"reauthRequiredAt,omitempty"`
	// ReauthReason is the provider's stated reason, e.g. "unauthorized".
	ReauthReason string `json:"reauthReason,omitempty"`
}

// ThreadStatus is the provider's own lifecycle state for a thread. It is NOT Tallpine's
// session status, which stays derived from durable facts at read time; this is one
// more such fact.
type ThreadStatus string

// Thread statuses. These mirror the provider's vocabulary rather than renaming it:
// a value Tallpine does not recognize is stored as-is rather than being flattened into
// something Tallpine does understand.
const (
	ThreadStatusActive      ThreadStatus = "active"
	ThreadStatusIdle        ThreadStatus = "idle"
	ThreadStatusNotLoaded   ThreadStatus = "not_loaded"
	ThreadStatusSystemError ThreadStatus = "system_error"
	ThreadStatusClosed      ThreadStatus = "closed"
)

// ConversationThreadState is what the provider says about the thread itself, as
// opposed to about a turn.
type ConversationThreadState struct {
	Status ThreadStatus `json:"status,omitempty"`
	// WaitingOn are the provider's active flags, e.g. "waiting_on_approval". A
	// thread can be active AND blocked on a person, and those are different states.
	WaitingOn []string `json:"waitingOn,omitempty"`
	// ArchivedAt is set while the provider considers the thread archived. Archiving
	// is reversible, so this returns to nil on unarchive rather than being a
	// one-way marker.
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`
	// ClosedAt is when the provider dropped the thread. Recorded rather than acted
	// on: Tallpine has never observed this notification, so tearing a controller down on
	// the strength of it would be guessing.
	ClosedAt *time.Time `json:"closedAt,omitempty"`
	// Compaction is the latest context compaction the provider reported, kept with
	// the thread state so it needs no column of its own.
	Compaction *ConversationCompaction `json:"compaction,omitempty"`
	UpdatedAt  time.Time               `json:"updatedAt"`
}

// CompactionStatus is where a context compaction stands.
type CompactionStatus string

const (
	// CompactionRunning is a compaction that has started and not finished.
	CompactionRunning CompactionStatus = "compacting"
	// CompactionCompleted is a compaction that finished.
	CompactionCompleted CompactionStatus = "completed"
	// CompactionFailed is a compaction the provider could not complete.
	CompactionFailed CompactionStatus = "failed"
)

// ConversationCompaction is the latest compaction of the provider's context.
// Token counts are zero when the provider did not report them.
type ConversationCompaction struct {
	// ID is the provider's id for the compaction, so a late report is only merged
	// into the compaction it belongs to.
	ID     string           `json:"id,omitempty"`
	Status CompactionStatus `json:"status"`
	// Trigger is "auto" or "manual"; empty while unknown.
	Trigger    string `json:"trigger,omitempty"`
	PreTokens  int64  `json:"preTokens,omitempty"`
	PostTokens int64  `json:"postTokens,omitempty"`
	// At is when the compaction started, or finished once it has.
	At time.Time `json:"at"`
}

// ConversationMCPServer is one tool server's startup state.
type ConversationMCPServer struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// Error is the provider's failure text, when it failed.
	Error string `json:"error,omitempty"`
	// FailureReason is the provider's classification, e.g.
	// "reauthenticationRequired", which is actionable in a way a message is not.
	FailureReason string `json:"failureReason,omitempty"`
}

// PlanStepStatus is where one plan step stands.
type PlanStepStatus string

// Plan step statuses. Tallpine's own snake_case spelling, not the provider's camelCase:
// these are persisted and read by clients, and the rest of the durable vocabulary
// here is snake_case.
const (
	PlanStepPending    PlanStepStatus = "pending"
	PlanStepInProgress PlanStepStatus = "in_progress"
	PlanStepCompleted  PlanStepStatus = "completed"
)

// ConversationPlan is the agent's current plan for a turn.
//
// Structured steps rather than a blob of text. The provider sends a list of steps
// each carrying its own status and re-sends the whole list on every change, so the
// structure is what it actually said; flattening it to prose would throw away the
// one thing a reader wants, which is where the agent is up to.
type ConversationPlan struct {
	// Explanation is the agent's note about the plan as a whole, when it gives one.
	Explanation string                 `json:"explanation,omitempty"`
	Steps       []ConversationPlanStep `json:"steps"`
	// SpansTurns marks a list the agent keeps across turns, such as Claude's task
	// list, rather than one plan per turn. A turn ending says nothing about its
	// steps, and the copy each later turn receives is the same list, not a new plan.
	SpansTurns bool `json:"spansTurns,omitempty"`
}

// ConversationPlanStep is one step of a plan.
type ConversationPlanStep struct {
	Text   string         `json:"text"`
	Status PlanStepStatus `json:"status"`
}

// ConversationSettings are the per-turn provider choices Tallpine remembers.
//
// Durable rather than client-side because they must apply to turns Tallpine dispatches
// on the user's behalf: a queued message draining after a restart, a relay from
// `tallpine send`. A preference held in the renderer would quietly stop applying the
// moment the user was not the one pressing send.
type ConversationSettings struct {
	// Model is the provider's model id. Empty means the provider's default.
	Model string `json:"model,omitempty"`
	// ReasoningEffort is how much thinking to spend, from the model's own list.
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	// FastMode asks for the provider's faster tier. It is held for the life of
	// the controller and not stored: a restart returns to the standard tier,
	// which is the cheaper way to be wrong.
	FastMode bool `json:"fastMode,omitempty"`
	// PlanMode asks the provider to plan rather than act, where Tallpine sets that per
	// turn. Like FastMode it is held for the life of the controller only.
	PlanMode bool `json:"planMode,omitempty"`
	// ApprovalMode is Tallpine's permission vocabulary, applied per turn.
	ApprovalMode PermissionMode `json:"approvalMode,omitempty"`
	// OpenCodeMode is the provider-owned mode explicitly selected through ACP.
	// It is separate from approval policy and restored before accepting turns.
	OpenCodeMode string `json:"openCodeMode,omitempty"`
	// AutoCompactWindow is where Claude Code compacts on its own: "200k" or
	// "1m", or a whole percent such as "40%". Empty leaves it to the Settings
	// default for the harness. Claude Code chats change it live where the ACP
	// bridge allows; otherwise it is read when the agent starts.
	AutoCompactWindow string `json:"autoCompactWindow,omitempty"`
	// AutoCompactPending is true while the running agent still compacts at an
	// older value than the one chosen (per session, else the Settings default),
	// so the new one applies after a restart. Read-only and never stored.
	AutoCompactPending bool `json:"autoCompactPending,omitempty"`
	// AutoCompactProjectTokens is the window the project's own environment sets
	// for the agent, which Auto leaves in force; 0 when it sets none. Read-only
	// and never stored.
	AutoCompactProjectTokens int `json:"autoCompactProjectTokens,omitempty"`
	// ContextCapacity is the model's real context window (catalog, never the
	// agent's report), 0 when unknown. CompactThreshold is where the chosen
	// auto-compact setting compacts, resolved against it; 0 for Auto or an
	// unknown capacity. ModelLabel is the catalog's name for the model. All
	// read-only and never stored.
	ContextCapacity  int64  `json:"contextCapacity,omitempty"`
	CompactThreshold int    `json:"compactThreshold,omitempty"`
	ModelLabel       string `json:"modelLabel,omitempty"`
}

// ModelUsage is one model's cumulative token spend in a conversation. Input
// excludes the cache counters, so the four counters add up without overlap.
type ModelUsage struct {
	Model               string `json:"model"`
	InputTokens         int64  `json:"inputTokens"`
	CacheCreationTokens int64  `json:"cacheCreationTokens"`
	CacheReadTokens     int64  `json:"cacheReadTokens"`
	OutputTokens        int64  `json:"outputTokens"`
	// Credits is the provider-billed spend for harnesses that report credits
	// instead of tokens (Kiro); zero otherwise.
	Credits float64 `json:"credits"`
	// ContextPercent is how full the context window is (0-100) when the
	// harness reported it; nil otherwise.
	ContextPercent *float64 `json:"contextPercent,omitempty"`
	// Turns counts the prompt responses that reported this model.
	Turns int64 `json:"turns"`
}
