package ports

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Optional driver interfaces. Each is feature-detected, so a driver that cannot do
// one of these simply does not offer it and Tallpine hides the affordance rather than
// showing a control that fails.
type (
	// ChatCompactor summarizes earlier history to reclaim context. Without it a
	// long conversation eventually cannot accept another turn at all.
	ChatCompactor interface {
		Compact(ctx context.Context) (ChatCompactionResult, error)
	}
	// ChatInstructedCompactor compacts with instructions on what the summary
	// must keep (Claude Code's "/compact <instructions>"). A compactor without it
	// compacts with its own default summary.
	ChatInstructedCompactor interface {
		CompactWithInstructions(ctx context.Context, instructions string) (ChatCompactionResult, error)
	}
	// ChatContextClearer opens a fresh provider conversation with the same launch
	// settings and moves this conversation onto it (chat /clear for a provider
	// with no in-place clear). commit durably binds the new id and runs before
	// the switch; an error from it leaves the conversation on its old id. The
	// caller must keep the conversation idle.
	ChatContextClearer interface {
		ClearContext(ctx context.Context, commit func(newProviderConversationID string) error) error
	}
	// ChatRollbacker discards history back to a turn, provider-side. This is undo:
	// it changes what the agent remembers, which is why it is a provider call and
	// not a UI filter.
	ChatRollbacker interface {
		Rollback(ctx context.Context, providerTurnID string) error
	}
	// ChatForker branches a conversation, so an alternative approach can be tried
	// without destroying the original. A nil anchor copies the whole thread; a
	// non-nil provider turn id copies through that turn, inclusive.
	ChatForker interface {
		Fork(ctx context.Context, lastProviderTurnID *string) (providerConversationID string, err error)
	}
	// ChatIsolatedForker creates the fork in the destination provider host. The
	// source host and its active writer remain untouched. An empty turn id
	// copies current context; the caller must keep the source quiescent.
	ChatIsolatedForker interface {
		ForkIntoHost(ctx context.Context, sourceProviderConversationID, lastProviderTurnID string, cfg ChatStartConfig) (ChatConversation, error)
	}
	// ChatNativeTurnID resolves a completed provider turn to its native turn ID.
	ChatNativeTurnID interface {
		NativeTurnID(providerTurnID string) string
	}
	// ChatForkDeleter removes only a provider fork ID positively registered to
	// a Tallpine side conversation. The caller must never pass a main thread ID.
	ChatForkDeleter interface {
		DeleteFork(ctx context.Context, providerConversationID string) error
	}
	// ChatInheritedHistory proves native ancestry and expresses the supplied
	// replay in an ancestor's ID namespace. Nil means ancestry is unverified.
	// Event order and content must be preserved; callers still verify each copy.
	ChatInheritedHistory interface {
		InheritedHistory(ctx context.Context, ancestor domain.ConversationBranch, events []ChatEvent) ([]ChatEvent, error)
	}
	// ChatRenamer sets or reads a human title the provider derived for the thread.
	ChatRenamer interface {
		SetTitle(ctx context.Context, title string) error
	}
	// ChatSkillLister enumerates the named skills a user can invoke.
	ChatSkillLister interface {
		ListSkills(ctx context.Context) ([]ChatSkill, error)
	}
	// ChatSandboxNetwork is implemented by a conversation whose provider runs
	// the agent in a sandbox that can withhold network access. Tallpine's own tools
	// that load agent pages must not give the agent more network than that.
	ChatSandboxNetwork interface {
		// SandboxAllowsNetwork reports whether the agent can reach the network
		// under the turn's permission mode; empty means the mode it launched with.
		SandboxAllowsNetwork(turnMode PermissionMode) bool
	}
	// ChatUsageReporter reads the account's quota position on demand, for the
	// cases where waiting for the next notification is too late to be useful.
	ChatUsageReporter interface {
		ReadRateLimits(ctx context.Context) (ChatRateLimits, error)
	}
	// ChatMCPReloader restarts the provider's tool servers. It exists because the
	// failure it addresses is not the agent's: a server that failed to start, or
	// whose config changed on disk, leaves the agent short of tools for the rest of
	// the conversation, and the only alternative is throwing the session away.
	ChatMCPReloader interface {
		ReloadMCPServers(ctx context.Context) (ChatMCPReloadResult, error)
	}
)

// ChatTurnRef identifies a turn the provider accepted.
type ChatTurnRef struct {
	ProviderTurnID string
}

// ChatDeferredTurnStarter is implemented by protocols whose prompt call is the
// whole lifetime of a turn rather than a quick "accepted" request. SendTurn
// prepares such a turn and returns its id; the controller calls StartDeferredTurn
// only after that id is durably bound to Tallpine's turn row. This prevents a fast
// provider notification from racing ahead of the correlation record needed to
// project it.
//
// Drivers whose SendTurn already receives a provider acknowledgement (Codex's
// app-server, for example) do not implement this interface.
type ChatDeferredTurnStarter interface {
	StartDeferredTurn(providerTurnID string) error
	// DiscardDeferredTurn releases a prepared turn when Tallpine could not persist its
	// correlation id. No provider request has started at this point.
	DiscardDeferredTurn(providerTurnID string)
}

// ChatDecisionOption is one choice the provider says is valid for a pending
// request. Providers vary the offered set per request — some do not offer a
// plain decline — so clients render from this list rather than a fixed enum.
type ChatDecisionOption struct {
	// ID is the stable identifier, e.g. "accept" or "acceptForSession".
	ID string
	// Label is a human-readable form when the provider supplies one.
	Label string
	// Kind is the provider-neutral consent meaning. Provider IDs and labels may
	// be opaque or localized, so clients must not infer approval semantics from
	// either one.
	Kind ChatDecisionKind
	// Raw is the provider's own encoding, preserved so a structured decision
	// (one carrying a policy amendment, say) can be echoed back exactly.
	Raw []byte
}

// ChatDecisionKind is the semantic effect of a provider-owned decision.
type ChatDecisionKind string

// Provider-neutral decision kinds exposed to chat clients.
const (
	ChatDecisionAllowOnce    ChatDecisionKind = "allow_once"
	ChatDecisionAllowAlways  ChatDecisionKind = "allow_always"
	ChatDecisionRejectOnce   ChatDecisionKind = "reject_once"
	ChatDecisionRejectAlways ChatDecisionKind = "reject_always"
)

// ChatDecision is the answer to a pending request.
type ChatDecision struct {
	ID string
	// Raw, when set, is sent verbatim instead of ID. Used for the object-shaped
	// decisions some providers offer.
	Raw []byte
	// ResolvedBy names a non-human answerer for the audit trail, for example
	// "orchestrator:<session id>". Empty means the user. It is recorded on the
	// approval's timeline row and never sent to the provider.
	ResolvedBy string
	// Note is the answerer's short reason, recorded with ResolvedBy.
	Note string
}

// ChatInputAction is the provider-neutral disposition of a structured input
// request. Keeping it typed makes the HTTP boundary and every driver share one
// vocabulary instead of validating repeated string literals independently.
type ChatInputAction string

const (
	// ChatInputActionAccept accepts a provider structured input request.
	ChatInputActionAccept ChatInputAction = "accept"
	// ChatInputActionDecline declines a provider structured input request.
	ChatInputActionDecline ChatInputAction = "decline"
	// ChatInputActionCancel cancels a provider structured input request.
	ChatInputActionCancel ChatInputAction = "cancel"
)

// Valid reports whether the action can cross the Chat port boundary.
func (a ChatInputAction) Valid() bool {
	return a == ChatInputActionAccept || a == ChatInputActionDecline || a == ChatInputActionCancel
}

// ChatInputMode distinguishes the two ACP elicitation shapes Tallpine supports.
type ChatInputMode string

const (
	// ChatInputModeForm represents an ACP form elicitation.
	ChatInputModeForm ChatInputMode = "form"
	// ChatInputModeURL represents an ACP URL elicitation.
	ChatInputModeURL ChatInputMode = "url"
)

// Valid reports whether the mode has a defined provider-neutral representation.
func (m ChatInputMode) Valid() bool {
	return m == ChatInputModeForm || m == ChatInputModeURL
}

// ChatInputResponse answers a structured user-input request. Content is only
// meaningful for accept and must match the provider-supplied restricted schema.
type ChatInputResponse struct {
	Action  ChatInputAction
	Content map[string]any
}

// ChatInputRequest is a provider-neutral structured question. Schema is a flat,
// restricted JSON Schema object for form mode. URL is only present for url mode
// and must never be opened without the user's explicit consent.
type ChatInputRequest struct {
	Mode          ChatInputMode
	Message       string
	Schema        map[string]any
	URL           string
	ElicitationID string
}

// ChatInputResponder is optional because not every machine protocol supports
// structured user input. Tallpine gates the route/UI on the negotiated capability.
type ChatInputResponder interface {
	ResolveInput(ctx context.Context, requestID string, response ChatInputResponse) error
}
