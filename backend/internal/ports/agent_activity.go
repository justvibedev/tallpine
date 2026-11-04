package ports

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// AgentExitDetectionMode describes how Tallpine learns that an agent CLI process
// ended while its terminal runtime remains alive.
type AgentExitDetectionMode string

const (
	// AgentExitDetectionSupervisor means Tallpine must wrap the CLI in its generic
	// process supervisor because the adapter has no reliable exit hook.
	AgentExitDetectionSupervisor AgentExitDetectionMode = "supervisor"
)

// AgentExitDetector is an optional adapter capability. Adapters that omit it
// keep their existing launch behavior.
type AgentExitDetector interface {
	ExitDetectionMode() AgentExitDetectionMode
}

// AgentPromptReadinessProvider is an optional capability for interactive
// adapters that receive their first task after startup. It lets Tallpine wait until a
// terminal UI is ready before injecting text through the runtime. When the
// adapter also implements TerminalActivityDetector, an authoritative idle
// detection takes precedence over the fallback text patterns.
type AgentPromptReadinessProvider interface {
	PromptReadinessHints(ctx context.Context, cfg LaunchConfig) (PromptReadinessHints, error)
}

// AgentAfterStartPromptBuilder is an optional capability for interactive
// adapters that need to combine launch-only context with the first user turn.
// Tallpine calls it exactly once per after-start delivery and sends the returned
// value only after the runtime is ready.
type AgentAfterStartPromptBuilder interface {
	BuildAfterStartPrompt(ctx context.Context, cfg LaunchConfig) (string, error)
}

// AgentAfterStartCommand selects native TUI state before Tallpine delivers the first
// task. Confirmation must come from the current terminal output, so an ignored
// command cannot silently launch a task with the wrong model.
type AgentAfterStartCommand interface {
	AfterStartCommand(ctx context.Context, cfg LaunchConfig) (string, error)
	AfterStartCommandApplied(cfg LaunchConfig, output string) bool
}

// TerminalActivityDetector derives activity only from authoritative terminal UI markers.
type TerminalActivityDetector interface {
	DetectTerminalActivity(output string) (domain.ActivityState, bool)
}

// TurnFailure is a provider turn that ended on an error, and when it ended.
type TurnFailure struct {
	At      time.Time
	Message string
	// Interrupted marks a turn the user interrupted (Esc) rather than one
	// that ended on an error.
	Interrupted bool
}

// TranscriptTurnFailureDetector is implemented by adapters whose harness can
// end a turn without firing the hook that reports the turn's end, while still
// recording that end in its native transcript. Codex runs its Stop hook only
// after a successful turn, and fires no Stop when Esc interrupts one; Claude
// Code fires no hook at all for an Esc interrupt. Without this such a turn
// left the session active until the stale-activity observer screen-proved it
// idle.
type TranscriptTurnFailureDetector interface {
	// LastTurnFailure reports the newest turn end recorded in the transcript
	// when that turn ended on an error or was interrupted and no later turn
	// has started. ok is false for a successful or still-running turn, and
	// for a transcript that is missing or not the harness's own.
	LastTurnFailure(ctx context.Context, transcriptPath string) (failure TurnFailure, ok bool, err error)
}

// TerminalActivityOptions lets an adapter declare how the shared observer
// should read its terminal and which native decision states it can report.
type TerminalActivityOptions struct {
	PreferStyledOutput bool
	SupportsBlocked    bool
}

// TerminalActivityOptionsProvider exposes an adapter's terminal observation needs.
type TerminalActivityOptionsProvider interface {
	TerminalActivityOptions() TerminalActivityOptions
}

// EmptyComposerDetector is an opt-in safety capability for unsolicited
// coordination sent to an already-running interactive agent. It must return
// true only when current terminal evidence positively proves that the active
// composer contains no human-authored draft. A stale activity=idle fact alone
// is insufficient because typing into a composer does not emit a lifecycle
// hook until the human submits it.
type EmptyComposerDetector interface {
	ComposerIsEmpty(output string) bool
}

// WaitingInputComposerReadiness is an opt-in capability for adapters where an
// empty composer authoritatively proves that a durable waiting_input state is
// safe for unsolicited delivery. EmptyComposerDetector alone is insufficient:
// other harnesses can render an empty composer beside a permission or
// structured-input boundary.
type WaitingInputComposerReadiness interface {
	EmptyComposerProvesWaitingInputReady() bool
}

// ContinuousTerminalActivityDetector is implemented by adapters whose TUI is
// the only authoritative source for some activity transitions. These adapters
// are sampled on every observer tick, including while idle or waiting for
// input, so terminal state can move in either direction.
type ContinuousTerminalActivityDetector interface {
	TerminalActivityDetector
	ContinuouslyDetectTerminalActivity() bool
}

// WaitingTerminalActivityDetector is implemented by non-continuous terminal
// detectors that can authoritatively recover from a durable waiting-input state.
type WaitingTerminalActivityDetector interface {
	TerminalActivityDetector
	ContinuouslyDetectTerminalActivityWhileWaiting() bool
}

// PromptReadinessHints describes when an after-start prompt should be sent.
// Empty patterns mean "send immediately" unless the adapter also implements
// TerminalActivityDetector, in which case Tallpine waits for an authoritative idle
// detection. A non-positive timeout always preserves immediate delivery.
type PromptReadinessHints struct {
	InitialDelay time.Duration
	Patterns     []string
	PollInterval time.Duration
	Timeout      time.Duration
	Lines        int
}

// AgentResolver maps a session's harness onto the Agent adapter that drives it,
// so the Session Manager can spawn (and restore) a different agent per session
// without depending on the concrete adapter registry. ok=false means no adapter
// is registered for that harness.
type AgentResolver interface {
	Agent(harness domain.AgentHarness) (Agent, bool)
}

// SubmitActivitySignaler is an OPTIONAL capability an Agent adapter may
// implement to report whether its harness emits a prompt-submit signal (one
// that flips Activity.State to active). Without it the confirm loop could
// never observe active and would only burn its budget on spurious Enter
// nudges.
//
// The Session Manager uses this as one half of the bounded Enter
// re-submission gate for both ordinary messages and switched-agent
// continuations; it also requires BlockedActivitySignaler before it will
// nudge — see harnessNudgeSafe.
type SubmitActivitySignaler interface {
	EmitsSubmitActivity() bool
}

// SemanticMessageAcceptanceSignaler is implemented only by TUI adapters whose
// native prompt hook returns the accepted prompt text to Tallpine. It lets internal
// durable senders correlate a specific message with provider acceptance.
type SemanticMessageAcceptanceSignaler interface {
	EmitsSemanticMessageAcceptance() bool
}

// BlockedActivitySignaler is an OPTIONAL capability an Agent adapter may
// implement to report whether its harness emits a decision-pause signal (a
// permission/approval prompt that flips Activity.State to blocked) AND can
// clear that state before the turn ends — which requires the pre/post-tool-
// use trio so lifecycle can correlate the approved tool's post with the dialog
// that blocked the session. The Enter-only nudge is only SAFE when this is
// true: a harness that submits but cannot report blocked leaves the confirm
// loop unable to tell an unsubmitted draft from a pending permission dialog,
// so an Enter meant to resubmit the draft could instead answer the dialog.
//
// Two adapters satisfy this today:
//
//   - claude-code installs the pre/post-tool-use trio that lets lifecycle
//     correlate the approved tool's post with the dialog and clear blocked
//     before the turn ends.
//   - kimchi installs the same trio and maps Notification(permission_prompt)
//     to ActivityBlocked. Unlike claude-code, kimchi has no separate
//     permission-request hook — the blocked signal arrives via a Notification
//     event whose payload carries tool_use_id, which the lifecycle correlator
//     matches against the inflight map populated by PreToolUse.
//
// codex maps permission-request to waiting_input and opts out (no tool trio →
// blocked could not be cleared). Adapters that later gain a correlatable
// blocked signal implement this interface to opt in.
type BlockedActivitySignaler interface {
	EmitsBlockedActivity() bool
}

// StartupInputReadinessSignaler is an OPTIONAL capability for a TUI adapter
// whose first lifecycle hook cannot arrive until native startup dialogs have
// cleared and the agent can safely accept pane input. Tallpine gates user and
// automation writes on FirstSignalAt only for adapters that opt in here;
// hookless adapters must remain usable without manufacturing a signal.
type StartupInputReadinessSignaler interface {
	FirstSignalProvesInputReady() bool
}

// ActiveTurnSteerer is an OPTIONAL capability an Agent adapter implements when
// submitting input while its harness is mid-turn STEERS the running turn rather
// than being swallowed, queued, or applied to a dialog. Tallpine uses it to decide
// whether an unsolicited coordination message may be written into an active
// session. Adapters that do not implement it are treated as unsafe to steer, so
// an unknown harness is only ever written to while idle.
type ActiveTurnSteerer interface {
	SteersActiveTurn() bool
}
