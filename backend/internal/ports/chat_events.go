package ports

import (
	"context"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ChatEventKind discriminates ChatEvent. These are provider-neutral: a driver
// translates its native notifications into this set and drops what has no
// meaning here rather than inventing semantics.
type ChatEventKind string

// The event kinds a driver emits.
const (
	ChatEventTurnStarted   ChatEventKind = "turn.started"
	ChatEventTurnCompleted ChatEventKind = "turn.completed"
	// ChatEventUserMessageCompleted is a settled user message recovered from the
	// provider's native history. Live sends are already durable before dispatch and
	// therefore never emit this event; history readers use it to reconstruct turns
	// Tallpine did not originally render (for example, work completed in the TUI).
	ChatEventUserMessageCompleted ChatEventKind = "message.user.completed"
	ChatEventMessageDelta         ChatEventKind = "message.delta"
	ChatEventMessageCompleted     ChatEventKind = "message.completed"
	ChatEventActivityStarted      ChatEventKind = "activity.started"
	ChatEventActivityCompleted    ChatEventKind = "activity.completed"
	ChatEventApprovalRequested    ChatEventKind = "approval.requested"
	ChatEventApprovalResolved     ChatEventKind = "approval.resolved"
	ChatEventInputRequested       ChatEventKind = "input.requested"
	ChatEventInputResolved        ChatEventKind = "input.resolved"
	ChatEventControllerState      ChatEventKind = "controller.state"
	ChatEventError                ChatEventKind = "error"

	// Kinds below carry provider signal Tallpine previously discarded. Each is modelled
	// rather than folded into an activity so a reader can tell "the agent produced
	// output" from "the conversation itself changed".

	// ChatEventCommandOutputDelta is streamed output from a running command. The
	// final aggregate can be incomplete, so a reader that wants the whole output
	// has to accumulate these.
	ChatEventCommandOutputDelta ChatEventKind = "command.output.delta"
	// ChatEventTurnDiff is the running diff of what the turn has changed on disk.
	ChatEventTurnDiff ChatEventKind = "turn.diff"
	// ChatEventCompacted reports that the provider summarized earlier history to
	// reclaim context. It is a fact about the conversation, not a turn.
	ChatEventCompacted ChatEventKind = "thread.compacted"
	// ChatEventUsage is a token accounting update for the conversation.
	ChatEventUsage ChatEventKind = "usage"
	// ChatEventRateLimits is the account's quota position, which is why a turn can
	// fail for reasons that have nothing to do with the request.
	ChatEventRateLimits ChatEventKind = "account.rateLimits"
	// ChatEventThreadRenamed reports a title the provider derived for the thread.
	ChatEventThreadRenamed ChatEventKind = "thread.renamed"
	// ChatEventPlanUpdated is the agent's current plan for the turn.
	ChatEventPlanUpdated ChatEventKind = "turn.plan"

	// ChatEventReasoningDelta is streamed reasoning text for a reasoning activity.
	// It is a delta rather than a message because reasoning is not prose the user is
	// being addressed with: it belongs to the activity it explains, and a reader
	// must be able to hide it without losing the answer.
	ChatEventReasoningDelta ChatEventKind = "reasoning.delta"
	// ChatEventCommandInput is a keystroke stream the agent sent INTO a running
	// command's terminal. Separate from command output on purpose: a PTY echoes what
	// is typed, so folding input into the output stream would print it twice, and a
	// transcript that cannot distinguish the two cannot say who did what.
	ChatEventCommandInput ChatEventKind = "command.input"
	// ChatEventActivityText is streamed provider prose for an activity that is not
	// reasoning: MCP tool-call progress, patch application output. Folded into the
	// same accumulation, which is why one kind covers both.
	ChatEventActivityText ChatEventKind = "activity.text"
	// ChatEventModelRerouted reports the provider answering with a model other than
	// the one asked for. Without it a client keeps naming the model the user chose
	// while a different one is answering.
	ChatEventModelRerouted ChatEventKind = "model.rerouted"
	// ChatEventAccountChanged reports the provider account's auth mode, plan, or its
	// need for credentials Tallpine does not hold.
	ChatEventAccountChanged ChatEventKind = "account.changed"
	// ChatEventThreadState reports the provider's own lifecycle view of the thread:
	// working, idle, archived, closed.
	ChatEventThreadState ChatEventKind = "thread.state"
	// ChatEventMCPServers reports the startup state of the tool servers this
	// conversation can reach, which is what explains a tool call that failed for a
	// reason the agent had no part in.
	ChatEventMCPServers ChatEventKind = "mcp.servers"
	// ChatEventAsyncTask reports a harness background task (a background Bash
	// command, a Monitor, or the harness waking itself for a turn) starting,
	// progressing or ending. It is observational: the task belongs to the
	// provider, Tallpine only needs to know the agent is waiting on it.
	ChatEventAsyncTask ChatEventKind = "async.task"
)

// Async task states, as the provider reports them.
const (
	ChatAsyncTaskRunning   = "running"
	ChatAsyncTaskPaused    = "paused"
	ChatAsyncTaskCompleted = "completed"
	ChatAsyncTaskFailed    = "failed"
	ChatAsyncTaskStopped   = "stopped"
)

// ChatAsyncTaskStopper is a live conversation that can stop one of its harness
// background tasks.
type ChatAsyncTaskStopper interface {
	// StopAsyncTask stops the task by its harness id. False means the harness
	// declined: the task already ended, or stopping it now would drop a queued
	// prompt.
	StopAsyncTask(ctx context.Context, taskID string) (stopped bool, err error)
}

// ChatAsyncTask is one harness background task. TaskType "turn" is the harness
// waking itself to work (not something the agent waits on); "monitor" is a
// Monitor watch; anything else is background command work.
type ChatAsyncTask struct {
	ID          string
	Name        string
	TaskType    string
	Description string
	// State is empty on a spawn that carries none; spawn means running.
	State   string
	Summary string
}

// Done reports whether the task reached a terminal state.
func (t ChatAsyncTask) Done() bool {
	return t.State == ChatAsyncTaskCompleted || t.State == ChatAsyncTaskFailed || t.State == ChatAsyncTaskStopped
}

// ChatControllerState is the health of the driver's connection to the provider.
type ChatControllerState string

// Controller states. Unknown is not death: a failed probe is not proof a session
// is gone, matching how Tallpine already treats runtime probes.
const (
	ChatControllerConnecting ChatControllerState = "connecting"
	ChatControllerReady      ChatControllerState = "ready"
	ChatControllerBusy       ChatControllerState = "busy"
	ChatControllerRecovering ChatControllerState = "recovering"
	ChatControllerHibernated ChatControllerState = "hibernated"
	ChatControllerStopped    ChatControllerState = "stopped"
)

type chatProviderFailure struct {
	message string
	cause   error
}

// NewChatProviderFailure preserves provider prose as opaque display text. An
// adapter may attach an existing sentinel (e.g. ErrChatAuthRequired) when native
// metadata proves it; callers never infer authentication recovery from the message.
func NewChatProviderFailure(title, detail string, cause error) error {
	title = strings.TrimSpace(title)
	detail = strings.TrimSpace(detail)
	if title == "" {
		title, detail = detail, ""
	}
	if title == "" {
		title = "Provider error"
	}
	if detail == title {
		detail = ""
	}
	if detail != "" {
		title += "\n\n" + detail
	}
	return &chatProviderFailure{message: title, cause: cause}
}

func (f *chatProviderFailure) Error() string { return f.message }
func (f *chatProviderFailure) Unwrap() error { return f.cause }

// ChatEvent is one normalized observation from the provider.
//
// Deltas are the high-frequency case, so they carry only what changed. A
// projector folds a delta into the message identified by ProviderItemID and
// bumps its revision; it never allocates a new timeline position per token.
// Assistant message text is portable Markdown. Adapters convert native citation
// annotations to links before emitting events or returning history.
type ChatEvent struct {
	Kind ChatEventKind
	// NativeUserMessageID is an adapter-proven native user record identity.
	// Unlike ProviderItemID, it is never synthesized or namespaced by Tallpine.
	NativeUserMessageID string
	// NativeTurnID is the provider's turn identity before Tallpine storage scoping.
	// Hooks use this identity to prove the replay includes their completed turn.
	NativeTurnID string
	// ProviderEventID is an identity for this exact native event, when the
	// provider supplies one. It is deliberately distinct from ProviderItemID:
	// start, delta and completion events commonly share one item id.
	ProviderEventID string

	// ProviderTurnID is set on every event that belongs to a turn.
	ProviderTurnID string
	// ProviderConversationID identifies the native thread/session the event came
	// from. It may differ from ChatConversation.ProviderConversationID for nested
	// agent work. Empty preserves compatibility with providers whose protocol has
	// no nested-conversation identity.
	ProviderConversationID string
	// ProviderItemID identifies the message or activity being reported.
	ProviderItemID string
	// ProviderItemAliases carries replay-only historical identities that referred
	// to the same item before an adapter introduced stronger namespacing. They are
	// reconciliation hints, not new durable provider identities.
	ProviderItemAliases []string
	// ClientMessageID is the provider-carried idempotency key for a recovered user
	// message, when one exists. History adapters synthesize a stable value when the
	// native protocol has no client identity.
	ClientMessageID string

	// TurnState is set on turn.completed.
	TurnState domain.TurnState

	// Delta is the text appended by a message.delta, a reasoning.delta, a
	// command.output.delta, a command.input or an activity.text.
	Delta string
	// Text is the settled text on message.completed, and the settled prose on an
	// activity that streamed text — a reasoning item's summary, once the provider
	// reports it. Empty means the provider settled nothing, which must not be read
	// as "the streamed text was wrong": a projection that replaced an accumulation
	// with an empty settle would erase reasoning the user watched arrive.
	Text string

	// ActivityKind and ActivityStatus are set on activity events.
	ActivityKind   domain.ActivityKind
	ActivityStatus domain.ActivityStatus
	// Summary is the one-line label for an activity or approval.
	Summary string
	// Detail is the provider-neutral typed payload, JSON-encoded.
	Detail []byte

	// RequestID and Decisions are set on approval.requested.
	RequestID string
	Decisions []ChatDecisionOption
	// Input is set on input.requested. It remains separate from Decisions because
	// a form response is structured data, not one provider-owned button id.
	Input *ChatInputRequest

	// ControllerState is set on controller.state.
	ControllerState ChatControllerState

	// Usage is set on usage events. Nil elsewhere.
	Usage *ChatUsage
	// RateLimits is set on account.rateLimits events. Nil elsewhere.
	RateLimits *ChatRateLimits
	// Diff is set on turn.diff.
	Diff *ChatTurnDiff
	// Title is set on thread.renamed.
	Title string

	// Plan is set on turn.plan. Nil elsewhere.
	Plan *domain.ConversationPlan
	// Reroute is set on model.rerouted.
	Reroute *ChatModelReroute
	// Account is set on account.changed.
	Account *ChatAccount
	// ThreadState is set on thread.state.
	ThreadState *ChatThreadState
	// MCPServers is set on mcp.servers. A single-server update carries one entry:
	// the provider reports servers one at a time, so a consumer merges by name
	// rather than replacing its whole list.
	MCPServers []ChatMCPServer
	// AsyncTask is set on async.task.
	AsyncTask *ChatAsyncTask

	// Err carries display text and optional typed causes. Its presence does not
	// imply the conversation is over; check Kind and ControllerState.
	Err error
}
