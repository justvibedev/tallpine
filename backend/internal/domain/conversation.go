package domain

import (
	"errors"
)

// The durable conversation model for Chat-mode sessions.
//
// Two deliberate shapes here, both learned from how existing chat products
// settle:
//
//   - Messages and activities are separate records, not one polymorphic "item"
//     row. Assistant text is mutated in place as deltas arrive; a command
//     execution or approval is written once with a typed payload. Those are
//     different write patterns and fighting them in one table costs more than
//     the extra type.
//
//   - Sequence is conversation-scoped, assigned on insert, and immutable.
//     Timestamps are display metadata: provider events arrive out of order and
//     several can share a millisecond, so ordering must not depend on them.
//
// The provider conversation remains authoritative for model context. These rows
// are authoritative for what Tallpine renders and for delivery state — Tallpine never
// maintains a second independently writable model transcript.

// ConversationOwnerKind keeps reviewer and worker controller ids in separate
// namespaces, even when their persisted strings happen to match.
type ConversationOwnerKind string

const (
	// ConversationOwnerSession identifies a worker-session conversation.
	ConversationOwnerSession ConversationOwnerKind = "session"
	// ConversationOwnerReview identifies a reviewer-owned conversation.
	ConversationOwnerReview ConversationOwnerKind = "review"
)

// ConversationOwner is the typed controller identity used by Chat lifecycle
// and persistence.
type ConversationOwner struct {
	Kind ConversationOwnerKind `json:"kind"`
	ID   string                `json:"id"`
}

// SessionConversationOwner builds the owner identity for a worker session.
func SessionConversationOwner(id SessionID) ConversationOwner {
	return ConversationOwner{Kind: ConversationOwnerSession, ID: string(id)}
}

// ReviewConversationOwner builds the owner identity for a reviewer chat.
func ReviewConversationOwner(id string) ConversationOwner {
	return ConversationOwner{Kind: ConversationOwnerReview, ID: id}
}

// ConversationScope says whether a conversation belongs to a project (the
// orchestrator narrative, which outlives any single orchestrator session) or to
// one session (a worker).
type ConversationScope string

// Conversation scopes.
const (
	ConversationScopeSession ConversationScope = "session"
	ConversationScopeProject ConversationScope = "project"
	ConversationScopeReview  ConversationScope = "review"
)

// ConversationContextResetProviderItemID returns the durable identity of the
// hidden activity that separates a fresh project orchestrator context from the
// project conversation history that came before it.
func ConversationContextResetProviderItemID(session SessionID) string {
	return "ao-context-reset:" + string(session)
}

// TurnState is the lifecycle of one request and the agent work that follows it.
type TurnState string

// Turn states. Interrupted is distinct from failed: the provider reports it as
// its own terminal status when a turn is cancelled, and Tallpine must not relabel it.
// Recovered means history proved the turn is no longer live but did not carry a
// portable provider outcome; it is terminal without claiming success or failure.
const (
	TurnStateQueued      TurnState = "queued"
	TurnStateRunning     TurnState = "running"
	TurnStateCompleted   TurnState = "completed"
	TurnStateRecovered   TurnState = "recovered"
	TurnStateInterrupted TurnState = "interrupted"
	TurnStateFailed      TurnState = "failed"
	// TurnStateCancelled marks a queued turn the user withdrew from the dock
	// before dispatch. Stop and handoff still settle the queue as interrupted.
	TurnStateCancelled TurnState = "cancelled"
)

// Terminal reports whether no further work is expected on the turn.
func (s TurnState) Terminal() bool {
	switch s {
	case TurnStateCompleted, TurnStateRecovered, TurnStateInterrupted, TurnStateFailed, TurnStateCancelled:
		return true
	default:
		return false
	}
}

// MessageRole distinguishes what the reader sees as their own text from the
// agent's.
type MessageRole string

// Message roles.
const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
)

// MessageOrigin records who produced a message. It is durable and structural:
// worker and automation identity is never inferred from a text prefix.
type MessageOrigin string

// Message origins.
const (
	MessageOriginHuman      MessageOrigin = "human"
	MessageOriginAutomation MessageOrigin = "automation"
	MessageOriginDaemon     MessageOrigin = "daemon"
	MessageOriginProvider   MessageOrigin = "provider"
)

// ActivityKind is the type of a non-message timeline entry. Provider-specific
// detail lives in the activity's typed payload, never in a new kind per
// provider event subtype.
type ActivityKind string

// Activity kinds. Reasoning is retained as its own kind so it can be excluded
// from display without parsing message text.
const (
	ActivityKindCommand    ActivityKind = "command"
	ActivityKindFileChange ActivityKind = "file_change"
	ActivityKindPlan       ActivityKind = "plan"
	ActivityKindReasoning  ActivityKind = "reasoning"
	ActivityKindApproval   ActivityKind = "approval"
	ActivityKindUsage      ActivityKind = "usage"
	ActivityKindError      ActivityKind = "error"
	ActivityKindSystem     ActivityKind = "system"
	// ActivityKindMCPTool is a tool call into an MCP server. Its own kind rather
	// than a command: an MCP call has a server, a tool name, structured arguments
	// and a structured result, and none of that is a shell invocation. Reporting it
	// as a command made every tool call render as if the agent had run something in
	// the worktree.
	ActivityKindMCPTool ActivityKind = "mcp_tool"
	// ActivityKindAutoReview is an automatic approval review: the provider decided,
	// on the user's behalf, whether an action was safe enough to run without asking.
	// Distinct from ActivityKindApproval, which is a question waiting on a person.
	// A decision made FOR the user is exactly the thing they need to be able to see
	// after the fact.
	ActivityKindAutoReview ActivityKind = "auto_review"
	// ActivityKindUserInput is a structured question the provider is waiting for
	// the person to answer. It is not an approval: accepting a tool and supplying
	// form data have different response contracts and different UI.
	ActivityKindUserInput ActivityKind = "user_input"
	// ActivityKindTallpineAction is a daemon-confirmed product mutation. The neutral
	// action name lives in detail, not in a new kind per operation, so Chat can
	// label the outcome without reading command text or tool prose.
	ActivityKindTallpineAction ActivityKind = "ao_action"
)

// ActivityStatus is the lifecycle of one activity. A provider may omit an item's
// completion when its enclosing turn stops, so the turn settlement path also
// settles any activity that is still running.
type ActivityStatus string

// Activity statuses.
const (
	ActivityStatusRunning   ActivityStatus = "running"
	ActivityStatusCompleted ActivityStatus = "completed"
	// ActivityStatusRecovered means replay proved the activity is historical,
	// but the provider supplied no portable success/failure outcome.
	ActivityStatusRecovered ActivityStatus = "recovered"
	ActivityStatusFailed    ActivityStatus = "failed"
	ActivityStatusCancelled ActivityStatus = "cancelled"
	ActivityStatusPending   ActivityStatus = "pending"
	ActivityStatusResolved  ActivityStatus = "resolved"
)

// ErrNoConversation reports that a session has no conversation row yet. It is not
// a failure: a Chat session has no conversation until its controller first
// starts, and a reader should show an empty conversation rather than an error.
var ErrNoConversation = errors.New("session has no conversation")

// ErrNoQueuedTurn reports that nothing is waiting to be sent. Draining an empty
// queue is the normal case, not an error.
var ErrNoQueuedTurn = errors.New("no queued turn")

// ErrSessionNotProvisioning rejects a pre-controller turn after startup ended.
var ErrSessionNotProvisioning = errors.New("session is not provisioning")

// ErrConversationRebound rejects a pre-controller turn aimed at a conversation the
// session no longer holds, such as an orchestrator's provisioning row after its
// first controller moved the queue to the project conversation. The caller should
// look the session's conversation up again.
var ErrConversationRebound = errors.New("conversation is no longer bound to the session")

// ErrNoConversationTurn reports a turn id that is not in the conversation it was
// named against. It lives here rather than in the storage layer so a controller and
// an HTTP handler can both recognize it without importing SQLite.
var ErrNoConversationTurn = errors.New("conversation turn not found")

// ErrClientMessageConflict refuses reuse of a delivery ID for different content.
var ErrClientMessageConflict = errors.New("client message id belongs to a different message")

// ErrConversationAutoResumeNotDue preserves consent when its deadline moved.
var ErrConversationAutoResumeNotDue = errors.New("usage-limit reset time has not arrived")

// ErrNoConversationBranch reports a branch id outside the named conversation.
var ErrNoConversationBranch = errors.New("conversation branch not found")
