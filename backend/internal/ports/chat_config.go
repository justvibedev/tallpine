package ports

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// ChatStartConfig is what a driver needs to open a new provider conversation.
type ChatStartConfig struct {
	// Policy is the agent policy decision for this conversation, computed once
	// by the session manager and frozen for its life. Drivers read their
	// permission effects from it; the zero value restricts nothing.
	Policy    agentpolicy.Decision
	SessionID domain.SessionID
	// DataDir is Tallpine's state root. Provider bindings may write process-scoped
	// configuration beneath it, but must never use the worktree or an OS-default
	// application-data directory for Tallpine-owned state.
	DataDir string
	// WorkspacePath is the session worktree. Always absolute: app-server-style
	// drivers resolve a relative path against their own process directory.
	WorkspacePath string
	// Env is the environment for the driver process and, transitively, for the
	// shell commands the agent runs. Tallpine passes a HookPATH-augmented copy so the
	// agent can invoke `tallpine` — that is how an orchestrator delegates.
	Env map[string]string
	// PrepareEnv rotates launch-only credentials. Persistent drivers defer it
	// until they know a new provider process is required; live adoption must keep
	// the verifier for the bearer already held by the surviving process.
	PrepareEnv func(context.Context) (map[string]string, error)
	// AcquireForkSource locks a current-context source immediately before the
	// provider copies it. Fork drivers release it before loading the child.
	AcquireForkSource func(context.Context) (copyCtx context.Context, release func(), err error)
	// RecordFork records an independent child before loading or activating it.
	RecordFork func(context.Context, string) error
	// Model is optional; empty defers to the provider's configured default.
	Model string
	// Effort is an optional provider-advertised model tuning value; empty
	// defers to the provider's configured default.
	Effort string
	// Permissions is Tallpine's existing per-session approval policy. Drivers map it
	// onto their provider's native approval and sandbox settings.
	Permissions PermissionMode
	// ReadOnly requires a provider-enforced sandbox that cannot modify the
	// workspace. It is used for review-owned conversations.
	ReadOnly bool
	// DisableNetwork requests tool network isolation for read-only background
	// conversations. Supporting drivers must not inherit reviewer network access.
	DisableNetwork bool
	// DisableTools requests text-only generation for background tasks from
	// providers that expose a native tool-selection option.
	DisableTools bool
	// SystemPrompt carries Tallpine's standing instructions for the session.
	SystemPrompt string
	// ProviderScopeID identifies the Tallpine ownership boundary for opaque provider
	// identifiers. Fresh approximate branches receive a new value.
	ProviderScopeID string
	// ProviderIDsScoped matches the branch's persisted ID format. False preserves
	// legacy projections written before scoped IDs were supported.
	ProviderIDsScoped bool
	// Ephemeral asks supporting providers not to persist this conversation.
	Ephemeral bool
	// AdditionalDirectories are extra absolute workspace roots the provider may
	// access alongside WorkspacePath. Workspace projects use this for child repo
	// worktrees; it is not a replacement for Tallpine's worktree ownership.
	AdditionalDirectories []string
	// MCPServers are client-supplied tool servers for this provider conversation.
	// User/provider configuration still loads normally; these are additive.
	MCPServers []ChatMCPServerConfig
}

// ChatResumeConfig reattaches to a provider conversation after a restart.
type ChatResumeConfig struct {
	// Policy is the agent policy decision for this resumed conversation; see
	// ChatStartConfig.Policy.
	Policy agentpolicy.Decision
	// ReconnectOnly forbids launching a replacement provider during a health check.
	ReconnectOnly bool
	// See ChatStartConfig.ProviderIDsScoped.
	ProviderIDsScoped      bool
	SessionID              domain.SessionID
	ProviderConversationID string
	// FreshIfMissing lets a driver that had to reload ProviderConversationID
	// start a fresh provider conversation when the provider reports it does not
	// exist. Callers set it only with durable proof that the conversation never
	// started. The returned conversation then reports the new id.
	FreshIfMissing bool
	DataDir        string
	WorkspacePath  string
	Env            map[string]string
	// See ChatStartConfig.PrepareEnv.
	PrepareEnv func(context.Context) (map[string]string, error)
	// Model is optional; empty keeps the provider conversation's current model.
	Model string
	// Effort is optional; empty keeps the provider conversation's current effort.
	Effort      string
	Permissions PermissionMode
	ReadOnly    bool
	// SystemPrompt is recomputed by the session manager on restore and reapplied
	// to the provider process. It is not persisted in the conversation transcript.
	SystemPrompt string
	// HasPriorTurns indicates whether the conversation already contains turns
	// (durable or provider history), so reconnection/restore does not re-send
	// standing instructions or session preamble intended only for the first turn.
	HasPriorTurns bool
	// ProviderScopeID identifies Tallpine's provider ownership boundary. A fresh
	// approximate branch must never inherit the parent's opaque-id scope.
	ProviderScopeID       string
	AdditionalDirectories []string
	MCPServers            []ChatMCPServerConfig
}

// ChatMCPServerConfig is the provider-neutral session-setup shape for a tool
// server supplied by Tallpine. Exactly one transport is meaningful according to Type.
// Secrets stay in Env/Headers and are sent only to the local provider process;
// this value is never part of a conversation snapshot.
type ChatMCPServerConfig struct {
	Name    string
	Type    string
	Command string
	Args    []string
	Env     map[string]string
	URL     string
	Headers map[string]string
}

// ChatInternalReplayResourceURI is reserved for Tallpine's reconstructed edit context.
const ChatInternalReplayResourceURI = "tallpine://conversation/edit-replay"

// ChatExcerptResourceURIPrefix identifies Tallpine-verified transcript excerpts. The
// daemon resolves these from durable messages; clients never supply the resource
// text directly.
const ChatExcerptResourceURIPrefix = "tallpine://conversation-excerpt/"

// ChatExcerptReference points at selected text in one durable transcript
// message. Revision makes stale selections fail closed when streaming updates
// replace the source text before the user sends their draft.
type ChatExcerptReference struct {
	ConversationID string `json:"conversationId"`
	MessageID      string `json:"messageId"`
	Revision       int64  `json:"revision"`
	Text           string `json:"text"`
}

// ChatExcerptContext is the server-verified context delivered for one selected
// transcript range. The paired turn is retained so a short selection can be
// understood without relying on hidden provider history.
type ChatExcerptContext struct {
	Reference        ChatExcerptReference `json:"reference"`
	SelectedText     string               `json:"selectedText"`
	UserMessage      string               `json:"userMessage"`
	AssistantMessage string               `json:"assistantMessage"`
}

// ChatContent is structured prompt context. Text remains on ChatUserMessage so
// the durable transcript has an ordinary readable message; these blocks enrich
// what the provider receives without leaking protocol DTOs above the adapter.
type ChatContent struct {
	Type     string              `json:"type"`
	Data     string              `json:"data,omitempty"`
	MIMEType string              `json:"mimeType,omitempty"`
	URI      string              `json:"uri,omitempty"`
	Name     string              `json:"name,omitempty"`
	Text     string              `json:"text,omitempty"`
	Excerpt  *ChatExcerptContext `json:"excerpt,omitempty"`
	// Internal distinguishes Tallpine-owned prompt context from a user attachment.
	// Public request DTOs never expose this bit; it is durable so edit/retry and
	// snapshot reconstruction can hide only content Tallpine actually synthesized.
	Internal bool `json:"internal,omitempty"`
}

// IsInternalReplayContent reports whether content is Tallpine's reconstructed-history
// seed rather than a resource supplied by the user.
func IsInternalReplayContent(content ChatContent) bool {
	return content.Internal && content.Type == "resource" && content.URI == ChatInternalReplayResourceURI
}

// ChatUserMessage is one inbound request to the agent.
type ChatUserMessage struct {
	// InteractionAt preserves initial acceptance across transition outbox replay.
	InteractionAt time.Time
	Text          string
	// SenderSessionID identifies the Tallpine session that authored an automation steer.
	// It is presentation metadata only and is never sent to the provider.
	SenderSessionID string
	// SenderProjectID and SenderDisplayName are resolved from SenderSessionID when
	// the source session is available. They are persisted on steer activities so
	// the renderer can show a stable label and safe Tallpine session link.
	SenderProjectID   string
	SenderDisplayName string
	// Content carries native images and resources for providers that negotiated
	// them. Drivers must reject an unsupported block rather than silently discard
	// context the user believed they sent.
	Content []ChatContent
	// ClientMessageID makes delivery idempotent: a retry with the same key must
	// not produce a second provider turn.
	ClientMessageID string
	// ClientPayloadHash identifies the original request before Tallpine adds reports
	// or other server-owned context. It is internal, never supplied by a client.
	ClientPayloadHash string
	// Excerpts are verified transcript selections attached to this user message.
	Excerpts []ChatExcerptReference
	// Origin records the timeline attribution and delivery source. Automation
	// shares the queue with the user and can never resolve an approval.
	Origin domain.MessageOrigin
	// AuthoredByUser distinguishes user-written content carried through Tallpine's
	// automation delivery path from content authored by automation itself.
	AuthoredByUser bool
	// Settings are the per-turn provider choices for this message. Zero means the
	// conversation's own defaults.
	Settings ChatTurnSettings
	// Continuation marks the user's one-click "continue" after stopping a turn,
	// so the timeline can show a marker instead of a message bubble.
	Continuation bool
}

// MessageDeliveryOptions describes facts about the message independent of the
// mechanism Tallpine uses to deliver it.
type MessageDeliveryOptions struct {
	InteractionAt time.Time
	// SenderSessionID is cooperative local identity, resolved from stored metadata.
	SenderSessionID string
	AuthoredByUser  bool
}

// MessageOutcome says what happened to a message Tallpine accepted for a session.
type MessageOutcome string

const (
	// MessageDelivered means Tallpine handed the message to the agent: it was written
	// to the terminal (which does not prove the agent has read or acted on it),
	// or the chat provider accepted it and is working on it.
	MessageDelivered MessageOutcome = "delivered"
	// MessageQueued means Tallpine accepted the message durably but the agent will
	// only see it later: after a running chat turn, or after an interface
	// transition settles.
	MessageQueued MessageOutcome = "queued"
	// MessageDuplicate means a message with the same client message id was
	// already accepted, so nothing new was sent; whether the original has been
	// delivered or is still queued is not reported.
	MessageDuplicate MessageOutcome = "duplicate"
)

// MessageReceipt reports the delivery outcome of one accepted message. ID is the
// handle a caller can correlate it by (chat turn id, queued message id, or the
// session-send delivery id); it is empty when the path has no stable handle.
type MessageReceipt struct {
	Outcome MessageOutcome
	ID      string
	// WaitingOnInput is set when the target's running turn was blocked on an
	// in-chat question when the message arrived. A delivered message was
	// steered into that turn; a queued one waits until the question is answered.
	WaitingOnInput bool
}

// ChatTurnSettings are the per-turn choices a provider accepts alongside the
// message text.
//
// Per turn, not per session: the provider takes these on every turn/start, so a
// user can change model or approval posture between messages without restarting
// the agent. Empty fields mean "use whatever the conversation was started with",
// which is what makes this additive — a caller that sets nothing behaves exactly
// as before.
type ChatTurnSettings struct {
	// Model is the provider's model id, from ChatModel.ID.
	Model string
	// Effort is how much reasoning to spend, from ChatModel.Efforts.
	Effort string
	// Approval is Tallpine's permission mode for this turn. The driver maps it onto
	// whatever approval policy and sandbox its provider understands.
	Approval PermissionMode
	// Fast asks for the provider's faster tier, on models that offer one.
	Fast bool
	// Plan asks the provider to plan the work rather than carry it out.
	Plan bool
}

// IsZero reports whether nothing was chosen, so a dispatch can omit the fields
// entirely rather than sending empty strings the provider would have to interpret.
func (s ChatTurnSettings) IsZero() bool {
	return s.Model == "" && s.Effort == "" && s.Approval == ""
}
