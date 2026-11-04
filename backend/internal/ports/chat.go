package ports

import (
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// The Chat controller contract.
//
// This is deliberately separate from the Agent/Runtime ports: those describe
// launching a provider's native CLI into a terminal and writing keystrokes at
// it. A Chat driver speaks a machine protocol and reports typed events. Stretching
// one contract over both would make every call site guess which one it has.
//
// Rules this port exists to keep:
//   - provider DTOs never escape the adapter; callers see domain vocabulary;
//   - terminal byte streams are never a source of Chat events;
//   - an empty user message is not "press Enter"; there is no keystroke concept;
//   - shell-terminal lifecycle is unrelated to conversation lifecycle.

// Errors a Chat driver returns. They map to stable API error codes, so a client
// can tell "this harness cannot do Chat" from "Codex is not logged in".
var (
	// ErrChatUnsupported means the harness has no Chat driver at all.
	ErrChatUnsupported = errors.New("chat mode unsupported for harness")
	// ErrChatDriverUnavailable means the driver exists but its binary or
	// bridge is missing.
	ErrChatDriverUnavailable = errors.New("chat driver unavailable")
	// ErrChatDriverIncompatible means the installed provider speaks a protocol
	// version Tallpine does not support.
	ErrChatDriverIncompatible = errors.New("chat driver incompatible")
	// ErrChatAuthRequired means the provider is installed but not authenticated.
	ErrChatAuthRequired = errors.New("chat driver requires authentication")
	// ErrChatAppRestart marks a turn cut short because the app or its chat host
	// went down, not because the person pressed stop. Its text is shown in the
	// chat and matched by the frontend label.
	ErrChatAppRestart = errors.New("the app restarted while the agent was working")
	// ErrChatResumeFailed means a stored provider conversation could not be
	// resumed. Callers must surface a recovery choice, never silently start a
	// new provider conversation.
	ErrChatResumeFailed = errors.New("chat conversation resume failed")
	// ErrChatRecoveryInconclusive means a detached provider host may still own
	// live work, but this daemon could not safely attach to it. Startup recovery
	// must preserve the durable session and worktree rather than treating the
	// failed attachment as proof that the provider died.
	ErrChatRecoveryInconclusive = errors.New("chat conversation recovery is inconclusive")
	// ErrChatHostNotRunning is a definitive observation that no provider host exists.
	ErrChatHostNotRunning = errors.New("chat provider host is not running")
	// ErrChatTurnNotAccepted is a definitive observation that a SendTurn failure
	// happened before the prompt could have reached the provider, so redelivering
	// it cannot run the work twice.
	ErrChatTurnNotAccepted = errors.New("chat turn was not accepted by the provider")
	// ErrChatNoActiveTurn means an interrupt found nothing to cancel — either Tallpine
	// has no turn in flight, or the provider no longer considers the named turn
	// active. A driver must translate its provider's refusal into this rather than
	// letting a protocol error escape: pressing stop a moment too late is an
	// ordinary thing for a person to do, not an internal failure.
	ErrChatNoActiveTurn = errors.New("no active turn to interrupt")
	// ErrChatRequestNotPending means the request a decision names is not waiting
	// for one: already answered, superseded, or from a controller that has been
	// replaced. Two clients looking at the same approval is normal, so one of them
	// arriving second is an ordinary outcome and not a failure.
	ErrChatRequestNotPending = errors.New("chat request is not pending")
	// ErrChatDecisionNotOffered means the decision is not one the provider offered
	// for that request. It must be refused and the request left pending: consent
	// Tallpine invented is not consent, and consuming the request on a bad decision
	// would leave the real answer with nothing to answer.
	ErrChatDecisionNotOffered = errors.New("chat decision was not offered for this request")
	// ErrChatConfigOptionInvalid means a client named an unknown option, sent the
	// wrong value type, or selected a value the provider did not advertise.
	ErrChatConfigOptionInvalid = errors.New("chat config option value is invalid")
	// ErrChatPermissionModeUnsupported means the requested Tallpine approval policy has
	// no enforced mapping in this provider. Drivers must return it instead of
	// silently running with a different permission policy.
	ErrChatPermissionModeUnsupported = errors.New("chat permission mode is unsupported")
	// ErrChatHistoryUnsettled means the native conversation history is not safe
	// to project as complete. Only a ChatHistoryRefresher promises that another
	// provider observation may make progress; rereading a snapshot need not.
	ErrChatHistoryUnsettled = errors.New("chat conversation history is not settled")
	// ErrChatHistoryUnavailable means the provider can resume its model context
	// but cannot replay that context as typed history. ACP session/resume has this
	// property; session/load is required when a caller needs a transcript replay.
	ErrChatHistoryUnavailable = errors.New("chat conversation history replay is unavailable")
	// ErrChatCapabilityUnavailable means an explicit tuning value was requested
	// but the provider did not advertise the matching live capability.
	ErrChatCapabilityUnavailable = errors.New("chat model capability unavailable")
	// ErrChatHistoryLoadFailed means one provider transcript load did not
	// produce a replay: the ACP session/load answered with a provider-side
	// failure (JSON-RPC -32603 "Internal error"), or a single load attempt ran
	// past Tallpine's per-attempt bound while the overall settle budget still had
	// time left. Neither is an unsettled Tallpine checkpoint, so callers must stop
	// re-sending the same load and report the failure instead of spending the
	// rest of the budget on it.
	ErrChatHistoryLoadFailed = errors.New("chat conversation history load failed")
)

// ChatCapabilityError reports why a harness cannot satisfy one session's Chat
// admission policy. It unwraps to ErrChatUnsupported so existing callers keep
// their stable error code while typed clients can render a safe recovery action
// without parsing the message.
type ChatCapabilityError struct {
	Harness                domain.AgentHarness
	Missing                []ChatCapability
	AllowedPermissionModes []PermissionMode
}

func (e *ChatCapabilityError) Error() string {
	return fmt.Sprintf("%s: %s lacks %v", ErrChatUnsupported, e.Harness, e.Missing)
}

func (e *ChatCapabilityError) Unwrap() error { return ErrChatUnsupported }

// ChatCapability names something a driver may or may not be able to do. Tallpine gates
// features on these rather than on the harness name.
type ChatCapability string

// The capabilities Tallpine currently checks.
const (
	ChatCapabilityStreaming ChatCapability = "streaming"
	ChatCapabilityTools     ChatCapability = "tools"
	ChatCapabilityApprovals ChatCapability = "approvals"
	ChatCapabilityInterrupt ChatCapability = "interrupt"
	ChatCapabilitySteer     ChatCapability = "steer"
	ChatCapabilityResume    ChatCapability = "resume"
	ChatCapabilityHistory   ChatCapability = "history"
	ChatCapabilityUsage     ChatCapability = "usage"
	ChatCapabilityDiffs     ChatCapability = "diffs"
	ChatCapabilityPlans     ChatCapability = "plans"
	// ChatCapabilityPlanMode is a plan mode Tallpine switches per turn, for a
	// provider that does not offer one as a session setting of its own.
	ChatCapabilityPlanMode ChatCapability = "plan_mode"
	// ChatCapabilityModels means the provider can enumerate the models it offers
	// and accept one per turn.
	ChatCapabilityModels ChatCapability = "models"
	// ChatCapabilityConfigOptions means the provider advertises live, typed
	// session controls such as model, thought level, mode, fast mode, or an agent
	// persona. The catalog is provider-owned and may change after any selection.
	ChatCapabilityConfigOptions ChatCapability = "config_options"
	// ChatCapabilityCompaction means the provider can summarize earlier history to
	// reclaim context.
	ChatCapabilityCompaction ChatCapability = "compaction"
	// ChatCapabilityRollback means history can be discarded back to a turn.
	ChatCapabilityRollback ChatCapability = "rollback"
	// ChatCapabilityFork means a conversation can be branched at a completed turn.
	ChatCapabilityFork ChatCapability = "fork"
	// ChatCapabilityForkCurrent copies the current provider context only; it
	// must not enable historical edits that require an exact turn cutoff.
	ChatCapabilityForkCurrent ChatCapability = "fork_current"
	// ChatCapabilityPromptReplay means Tallpine can open a fresh provider session with
	// a durable textual transcript supplied as context. This is an approximation
	// of fork for providers whose protocol cannot fork from a historical turn.
	ChatCapabilityPromptReplay ChatCapability = "prompt_replay"
	// ChatCapabilityRename means the thread carries a title Tallpine can set.
	ChatCapabilityRename ChatCapability = "rename"
	// ChatCapabilitySkills means named skills can be enumerated and invoked.
	ChatCapabilitySkills ChatCapability = "skills"
	// ChatCapabilityRateLimits means the account's quota position is readable.
	ChatCapabilityRateLimits  ChatCapability = "rate_limits"
	ChatCapabilityInteractive ChatCapability = "user_input"
	// ChatCapabilityMCPReload means the provider's tool servers can be restarted
	// without restarting the conversation. Named so a client can gate the control
	// rather than offering it and reading the refusal.
	ChatCapabilityMCPReload ChatCapability = "mcp_reload"
	// ChatCapabilityImages means the provider accepts native image content in a
	// prompt. Tallpine may still stage a worktree copy for durable/local context, but it
	// must not reduce an image to a path when this capability is available.
	ChatCapabilityImages ChatCapability = "images"
	// ChatCapabilityEmbeddedContext means the provider accepts embedded resources
	// (for example the contents selected through an @ mention) in a prompt.
	ChatCapabilityEmbeddedContext ChatCapability = "embedded_context"
	// ChatCapabilityResourceLinks means the provider preserves URI/name resource
	// links as native prompt content rather than silently reducing them to text.
	ChatCapabilityResourceLinks ChatCapability = "resource_links"
	// ChatCapabilityElicitation means the provider can stop a turn to request a
	// structured form or an explicitly-consented external URL interaction.
	ChatCapabilityElicitation ChatCapability = "elicitation"
	// ChatCapabilityNestedAgents means child-agent activity carries its parent tool
	// id, allowing clients to preserve the hierarchy instead of flattening it into
	// the top-level command stream.
	ChatCapabilityNestedAgents ChatCapability = "nested_agents"
	// ChatCapabilityTerminalOutput means command results include the provider's
	// terminal identity/output metadata. It is read-only transcript richness, not
	// permission for the provider to execute through Tallpine's terminal runtime.
	ChatCapabilityTerminalOutput ChatCapability = "terminal_output"
)

// ChatCapabilities is the set a driver reports from Probe.
type ChatCapabilities map[ChatCapability]bool

// Has reports whether the capability is present and enabled.
func (c ChatCapabilities) Has(capability ChatCapability) bool { return c[capability] }

// chatProductionFloor is the minimum a driver must support before Tallpine will let a
// session that can mutate a workspace run in Chat mode. Without approvals a
// mutating agent has no gate; without interrupt the user cannot stop it; without
// resume a daemon restart silently loses the conversation.
var chatProductionFloor = []ChatCapability{
	ChatCapabilityStreaming,
	ChatCapabilityApprovals,
	ChatCapabilityInterrupt,
	ChatCapabilityResume,
}

// MissingProductionCapabilities returns the floor capabilities a driver lacks.
// An empty result means the driver is Chat-ready.
func MissingProductionCapabilities(caps ChatCapabilities) []ChatCapability {
	var missing []ChatCapability
	for _, want := range chatProductionFloor {
		if !caps.Has(want) {
			missing = append(missing, want)
		}
	}
	return missing
}

// MissingCapabilitiesForPermissions applies the production floor for a
// specific session permission mode. An explicit bypass-permissions choice does
// not require an approval channel because the user has opted out of approvals.
func MissingCapabilitiesForPermissions(caps ChatCapabilities, permissions PermissionMode) []ChatCapability {
	missing := MissingProductionCapabilities(caps)
	if NormalizePermissionMode(permissions) != PermissionModeBypassPermissions {
		return missing
	}
	out := missing[:0]
	for _, capability := range missing {
		if capability != ChatCapabilityApprovals {
			out = append(out, capability)
		}
	}
	return out
}

// PermissionModesStrictestFirst is Tallpine's permission modes from the one that asks
// before every action to the one that never asks.
var PermissionModesStrictestFirst = []PermissionMode{
	PermissionModeDefault, PermissionModeAcceptEdits, PermissionModeAuto, PermissionModeBypassPermissions,
}
