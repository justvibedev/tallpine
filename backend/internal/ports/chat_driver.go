package ports

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ChatInstallChecker is implemented by chat drivers that can tell, without
// starting anything, that the adapter they need is missing.
type ChatInstallChecker interface {
	CheckChatInstall(ctx context.Context) error
}

// ChatPermissionModeChecker is implemented by chat drivers whose agent refuses
// some Tallpine permission modes, so a caller can pick one it accepts before starting.
type ChatPermissionModeChecker interface {
	SupportsPermissionMode(mode PermissionMode) bool
}

// ChatDriver opens conversations for one harness.
type ChatDriver interface {
	// Harness is the agent this driver serves.
	Harness() domain.AgentHarness
	// Probe checks the local install without creating anything: is the binary
	// present, is it authenticated, what can it do. It must be safe to call
	// before any durable session or worktree exists, so an unsupported request
	// can be rejected before Tallpine commits resources.
	Probe(ctx context.Context) (ChatCapabilities, error)
	// Start opens a new provider conversation.
	Start(ctx context.Context, cfg ChatStartConfig) (ChatConversation, error)
	// Resume reattaches to an existing one. It returns ErrChatResumeFailed
	// rather than silently starting a new conversation.
	Resume(ctx context.Context, cfg ChatResumeConfig) (ChatConversation, error)
}

// ChatDriverReconnector attaches only to a surviving provider. Implementations
// must never create a provider process or fall back to native history resume.
type ChatDriverReconnector interface {
	Reconnect(context.Context, ChatResumeConfig) (ChatConversation, error)
}

// ChatConversation is one live controller. Exactly one exists per Chat session,
// and it is the only writer to its provider conversation.
type ChatConversation interface {
	// ProviderConversationID is the opaque identifier Tallpine persists so a later
	// process can resume this conversation.
	ProviderConversationID() string
	// Capabilities is what this conversation actually negotiated, which can be
	// narrower than what Probe reported.
	Capabilities() ChatCapabilities
	// SendTurn delivers a user or automation message. Calls are serialized by
	// the controller: only one dispatch mutates the provider at a time.
	SendTurn(ctx context.Context, msg ChatUserMessage) (ChatTurnRef, error)
	// Interrupt cancels an in-flight turn.
	Interrupt(ctx context.Context, providerTurnID string) error
	// ResolveRequest answers a pending approval or user-input request. It is a
	// typed action: it is never derived from sending a message.
	ResolveRequest(ctx context.Context, requestID string, decision ChatDecision) error
	// Events is the normalized event stream. It closes when the conversation
	// ends. A consumer that falls behind is disconnected rather than allowed to
	// block the provider reader.
	Events() <-chan ChatEvent
	// Close releases the controller. It does not delete provider-side history.
	Close() error
}

// ChatContextBreakdownReader is optionally implemented by a conversation whose
// provider can report what fills its context window by category.
type ChatContextBreakdownReader interface {
	// ContextBreakdown asks the provider for its own breakdown. It is an explicit,
	// on-demand request (the provider may count each category with a model API).
	// supported=false, with a nil error, means the provider has no breakdown to
	// give, so callers show the totals only.
	ContextBreakdown(ctx context.Context) (breakdown domain.ContextBreakdown, supported bool, err error)
}

// ChatCompactionWindowSetter is optionally implemented by a conversation whose
// provider can change its auto-compaction window while running.
type ChatCompactionWindowSetter interface {
	// SetAutoCompactWindow moves the live window to tokens (0 clears Tallpine's value
	// and returns to the provider's own default). It reports applied=false, with
	// a nil error, when the provider cannot change it live: the caller then
	// leaves the new value for the next restart. It never fails the chat.
	SetAutoCompactWindow(ctx context.Context, tokens int) (applied bool, err error)
}

// ChatProviderPreserver is optionally implemented when Close only detaches the
// daemon-side controller and deliberately leaves provider work alive.
type ChatProviderPreserver interface {
	PreservesProviderOnClose() bool
}

// ChatProviderTerminator is optionally implemented when explicit session
// destruction must do more than detach the controller.
type ChatProviderTerminator interface {
	Terminate() error
}

// ChatProviderHibernator stops the controller and provider process while
// retaining the native conversation for a later Resume.
type ChatProviderHibernator interface {
	// CanHibernate checks provider-owned work that can outlive a settled turn.
	// An error must leave the provider running.
	CanHibernate(ctx context.Context) (bool, error)
	Hibernate() error
}

// ChatLiveReconnector identifies attachment to the same initialized provider
// process, as distinct from native resume in a replacement process.
type ChatLiveReconnector interface {
	ReconnectedLive() bool
}

// ChatLiveReconnectActivator releases provider replay only after the service
// has restored the durable active-turn correlation for the same live process.
type ChatLiveReconnectActivator interface {
	ActivateLiveReconnect(ctx context.Context, providerTurnID string) error
}

// ChatProviderEventAcknowledger lets a persistent provider discard replay
// state only after the controller commits the matching event.
type ChatProviderEventAcknowledger interface {
	AcknowledgeProviderEvent(ctx context.Context, providerEventID string) error
}

// ChatDriverRegistry resolves the driver for a harness.
type ChatDriverRegistry interface {
	// Driver returns the driver for a harness, or ErrChatUnsupported.
	Driver(harness domain.AgentHarness) (ChatDriver, error)
	// SupportsChat reports whether a harness has a Chat driver registered at
	// all, without probing the local install.
	SupportsChat(harness domain.AgentHarness) bool
}
