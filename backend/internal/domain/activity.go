package domain

import "time"

// ActivityState is how busy the agent is, reported via the agent's CLI hook
// callbacks, not inferred from transcript/JSONL
type ActivityState string

// Activity states. WaitingInput and Blocked are sticky (see IsSticky).
//
// WaitingInput and Blocked both mean "paused on the user" but demand opposite
// automation: waiting_input is an agent at an empty prompt awaiting its next
// INSTRUCTION (safe to message or nudge), while blocked is an agent stopped on
// a pending DECISION — a tool-permission or approval dialog — where a stray
// keystroke could answer the dialog on the user's behalf. Automated senders
// must never inject input into a blocked session. (Not to be confused with the
// PR-stack Blocked flag in the status read model; blocked here predates it —
// the state existed in the original activity model and returns with the
// permission-prompt producers.)
const (
	ActivityActive       ActivityState = "active"
	ActivityIdle         ActivityState = "idle"
	ActivityWaitingInput ActivityState = "waiting_input"
	ActivityBlocked      ActivityState = "blocked"
	ActivityExited       ActivityState = "exited"
)

// IsSticky reports whether an activity state must NOT be aged/demoted by the
// passage of time (a paused agent is still paused until a new signal says so).
func (a ActivityState) IsSticky() bool {
	return a == ActivityWaitingInput || a == ActivityBlocked
}

// NeedsInput reports whether the agent is paused on the user — waiting for the
// next instruction (waiting_input) or blocked on a decision (blocked). Both
// render as the needs_input session status. Distinct from IsSticky: stickiness
// is about time-demotion, NeedsInput about the user being the unblocker.
func (a ActivityState) NeedsInput() bool {
	return a == ActivityWaitingInput || a == ActivityBlocked
}

// IsBusy reports whether the agent is mid-turn (active). A paused agent
// (NeedsInput) or an idle one is not busy.
func (a ActivityState) IsBusy() bool { return a == ActivityActive }

// Activity captures the persisted activity reading: the state and when it was
// last observed.
//
// LastActivityAt is turn-level, not a heartbeat: it marks when State was last
// entered (an activity transition), and same-state repeats do not advance it
// (see lifecycle sameActivity). A chat worker's long turn that runs many tool
// calls therefore keeps the timestamp from the start of the turn. Do not use
// it as a liveness or stuck signal while State is active; use runtime probes
// or live provider work instead. Where it is compared to a window it answers
// "how long since the last transition", e.g. idle-for or quiet-since checks.
type Activity struct {
	State          ActivityState `json:"state"`
	LastActivityAt time.Time     `json:"lastActivityAt"`
}

// TurnFailureCandidate is a live terminal session in a turn (active or
// waiting_input) with a native transcript path: the narrow read the failed-turn
// observer polls instead of every session row.
type TurnFailureCandidate struct {
	ID                   SessionID
	Harness              AgentHarness
	Activity             Activity
	Revision             int64
	RuntimeLaunchID      string
	NativeTranscriptPath string
	// LatestUserPromptAt is the newest human prompt submission. A prompt
	// submitted while the session already read active leaves
	// Activity.LastActivityAt where the earlier turn started.
	LatestUserPromptAt time.Time
}

// AgentExitReason records why a session's agent last exited. Only an
// involuntary exit is resumed automatically; an unknown reason (older rows)
// is treated as the user's and stays stopped until an explicit Resume.
type AgentExitReason string

const (
	// AgentExitUnknown is a session with no recorded exit reason.
	AgentExitUnknown AgentExitReason = ""
	// AgentExitUser is a user's Exit Agent.
	AgentExitUser AgentExitReason = "user"
	// AgentExitHostLost is an agent whose provider host died with the daemon
	// (app quit, crash) while the agent was live.
	AgentExitHostLost AgentExitReason = "host_lost"
)
