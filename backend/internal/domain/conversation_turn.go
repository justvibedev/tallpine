package domain

import (
	"time"
)

// ConversationTurn is one user or automation request plus the agent work it
// caused.
type ConversationTurn struct {
	// ContextClearedActivityID is set only on the result of a /clear the service
	// carried out itself: no turn was created, and this is the transcript marker.
	ContextClearedActivityID string `json:"-"`
	ID                       string `json:"id"`
	ConversationID           string `json:"conversationId"`
	// BranchID is durable provider-lineage metadata used to keep opaque turn ids
	// inside the provider ownership epoch that created them.
	BranchID string `json:"-"`
	// HandledBySessionID is the Tallpine session whose controller ran the turn. For a
	// project-scoped conversation this changes when the orchestrator is
	// replaced; the conversation identity does not.
	HandledBySessionID SessionID `json:"handledBySessionId"`
	// HandledByReviewID is set when the typed reviewer Chat controller owns the
	// turn; worker and project conversations leave it empty.
	HandledByReviewID string `json:"handledByReviewId,omitempty"`
	// ProviderTurnID correlates back to the provider's own turn. Opaque.
	ProviderTurnID string `json:"providerTurnId,omitempty"`
	// ProviderInputText identifies a combined native prompt during history replay.
	// The public transcript continues to use the individual message rows.
	ProviderInputText string `json:"-"`
	// RetryOfTurnID is the failed source whose durable prompt created this turn.
	// It remains present after rollback so the source cannot offer a dead action.
	RetryOfTurnID string `json:"retryOfTurnId,omitempty"`
	// HasRetryAttempt is a snapshot-only fact derived from every durable retry
	// relation, including attempts outside the active provider branch.
	HasRetryAttempt bool `json:"hasRetryAttempt,omitempty"`
	// HasHumanPrompt is a snapshot-only fact independent of timeline pagination.
	HasHumanPrompt bool      `json:"hasHumanPrompt,omitempty"`
	State          TurnState `json:"state"`
	// ErrorMessage is set for failed turns. Interrupted turns are not errors.
	ErrorMessage string                  `json:"errorMessage,omitempty"`
	UsageLimit   *ConversationUsageLimit `json:"usageLimit,omitempty"`
	RequestedAt  time.Time               `json:"requestedAt"`
	StartedAt    *time.Time              `json:"startedAt,omitempty"`
	CompletedAt  *time.Time              `json:"completedAt,omitempty"`
	// RolledBackAt is set when a rollback discarded this turn provider-side. The
	// row survives because Tallpine does not destroy durable facts, but the agent no
	// longer remembers the exchange, so its messages and activities are left out
	// of the timeline. The turn itself stays readable so a client can say how much
	// an undo took back rather than letting the history silently shrink.
	RolledBackAt *time.Time `json:"rolledBackAt,omitempty"`
	// Diff is what the turn has changed on disk so far. Nil when the provider has
	// reported nothing, which is not the same as "changed nothing": a provider
	// without diff support never reports at all.
	Diff *ConversationTurnDiff `json:"diff,omitempty"`
	// Plan is the agent's current plan for this turn. Per-turn state rather than a
	// stack of timeline entries, for the same reason Diff is: the provider re-sends
	// the whole plan on every change, so only the latest is an answer and the
	// earlier ones are the same plan with fewer steps ticked off.
	Plan *ConversationPlan `json:"plan,omitempty"`
}

// ConversationTurnDiff is the turn's changed-file summary.
//
// Per-turn state rather than a timeline entry: the provider re-sends the whole
// diff on each update, so this is overwritten, and a reader sees the current
// answer instead of a stack of repeats.
type ConversationTurnDiff struct {
	Files []ConversationDiffFile `json:"files"`
	// Truncated reports that the file list was cut at Tallpine's cap. A cut list shown
	// as a whole one would understate the size of the change.
	Truncated bool `json:"truncated,omitempty"`
}

// ChatDiffTruncatedSummary marks a turn-diff event whose file list the driver had
// to cut at its cap.
//
// It travels in the event's Summary because ports.ChatTurnDiff has no field for
// it, and the fact cannot simply be dropped: a cut list rendered as a complete one
// would understate the size of the change. Declared here so the adapter that sets
// it and the projection that reads it share one spelling rather than two string
// literals that can drift apart silently.
const ChatDiffTruncatedSummary = "diff file list truncated"

// ConversationDiffFile is one changed path in a turn diff.
type ConversationDiffFile struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	// Status is the change kind: added, modified, deleted, renamed.
	Status string `json:"status"`
	// OldPath is set only for a rename.
	OldPath string `json:"oldPath,omitempty"`
	// RolledBackAt is set when a rollback discarded this turn provider-side. The
	// row survives because Tallpine does not destroy durable facts, but the agent no
	// longer remembers the exchange, so its messages and activities are left out
	// of the timeline. The turn itself stays readable so a client can say how much
	// an undo took back rather than letting the history silently shrink.
	RolledBackAt *time.Time `json:"rolledBackAt,omitempty"`
}

// QueuedTurn is a turn that was recorded but never dispatched, paired with the
// text that still has to be sent.
//
// A message the user typed while the agent was busy is durable before it is
// delivered, so the queue survives a daemon restart as data rather than living
// only in a controller's memory.
type QueuedTurn struct {
	TurnID string
	Text   string
	// ClientMessageID is carried through to the provider so a dispatch retried
	// after a crash cannot produce a second provider turn.
	ClientMessageID   string
	Origin            MessageOrigin
	SenderSessionID   string
	SenderProjectID   string
	SenderDisplayName string
	// DeliveryContentJSON carries provider-neutral native prompt blocks through
	// the durable queue. It is not rendered and never contains provider DTOs.
	DeliveryContentJSON string
}

// RetryPrompt is the durable human-authored content of a failed turn that may
// be dispatched again. Unlike QueuedTurn, its source was already sent and
// settled; ActiveLineage says whether it still belongs to the visible branch.
type RetryPrompt struct {
	Continuation        bool
	Text                string
	ProviderInputText   string
	Origin              MessageOrigin
	DeliveryContentJSON string
	ActiveLineage       bool
}

// ConversationMessage is one readable block of text.
type ConversationMessage struct {
	// InteractionAt is an internal write fact, independent of history CreatedAt.
	InteractionAt  time.Time `json:"-"`
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId"`
	TurnID         string    `json:"turnId,omitempty"`
	Sequence       int64     `json:"sequence"`
	// Revision increases each time streaming rewrites this message's text, so a
	// client can detect a gap and resync instead of rendering stale text.
	Revision int64         `json:"revision"`
	Role     MessageRole   `json:"role"`
	Origin   MessageOrigin `json:"origin"`
	Text     string        `json:"text"`
	// Continuation marks the user's one-click "continue" after stopping a turn.
	// The agent receives Text like any message; the timeline shows a marker
	// instead of a message bubble.
	Continuation bool `json:"continuation,omitempty"`
	// AuthoredByUser is an intake-only fact used to project user activity when
	// Tallpine delivered the message as automation. It is not part of the persisted
	// delivery origin or the conversation API representation.
	AuthoredByUser bool `json:"-"`
	// Streaming is true while more deltas are expected.
	Streaming bool `json:"streaming"`
	// ProviderItemID deduplicates provider observations of the same message.
	ProviderItemID string `json:"providerItemId,omitempty"`
	// ClientMessageID is the caller-supplied idempotency key for user messages.
	// A retry carrying the same key must not create a second provider turn.
	ClientMessageID     string `json:"clientMessageId,omitempty"`
	ClientPayloadHash   string `json:"-"`
	DeliveryContentJSON string `json:"-"`
	// Sender metadata is presentation data for automation-origin messages. The
	// display name is intentionally a send-time snapshot; the session id is stable.
	SenderSessionID   string    `json:"senderSessionId,omitempty"`
	SenderProjectID   string    `json:"senderProjectId,omitempty"`
	SenderDisplayName string    `json:"senderDisplayName,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// ConversationActivity is one non-message timeline entry: a command, a diff, a
// plan, an approval request, a usage report, an error.
type ConversationActivity struct {
	ID             string         `json:"id"`
	ConversationID string         `json:"conversationId"`
	TurnID         string         `json:"turnId,omitempty"`
	Sequence       int64          `json:"sequence"`
	Revision       int64          `json:"revision"`
	Kind           ActivityKind   `json:"kind"`
	Status         ActivityStatus `json:"status"`
	// Summary is the one-line label a client renders when collapsed.
	Summary string `json:"summary"`
	// Detail is the typed provider-neutral payload for this kind, as JSON. It
	// never carries provider DTOs verbatim.
	Detail []byte `json:"detail,omitempty"`
	// RequestID is the provider's request identifier for approval activities.
	// Resolving an approval matches on this, so a stale card cannot resolve a
	// newer request.
	RequestID      string    `json:"requestId,omitempty"`
	ProviderItemID string    `json:"providerItemId,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	// CommandOutput is streamed command output accumulated from output deltas.
	// Held outside Detail because it is appended to many times per command, while
	// Detail is written whole.
	//
	// Its presence does not make the output complete: the provider was measured
	// dropping the first chunk from the delta stream and the aggregate alike. What
	// it buys is output while the command is still running, and output at all for a
	// command that never completes.
	CommandOutput string `json:"commandOutput,omitempty"`
	// CommandOutputTruncated reports that accumulation stopped at Tallpine's cap.
	CommandOutputTruncated bool `json:"commandOutputTruncated,omitempty"`
	// StreamedText is prose the PROVIDER streamed for this activity, accumulated
	// from deltas. What it means depends on Kind, because each kind has exactly one
	// such stream: reasoning summary text for a reasoning activity, the keystrokes
	// the agent sent to a PTY for a command, patch output for a file change,
	// progress messages for an MCP tool call.
	//
	// Deliberately not merged into CommandOutput. That column is a program's own
	// bytes -- capped, ANSI-laden, and admitted to be incomplete -- and a reader
	// must be able to tell "the agent thought this" or "the agent typed this" from
	// "the program printed this". They also settle differently: command output is
	// only ever appended to, while streamed reasoning is REPLACED by the settled
	// summary when the item completes.
	StreamedText string `json:"streamedText,omitempty"`
	// StreamedTextTruncated reports that accumulation stopped at Tallpine's cap.
	StreamedTextTruncated bool `json:"streamedTextTruncated,omitempty"`
}
