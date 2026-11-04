// Package activitydispatch is the single source of truth mapping the agent
// token in `tallpine hooks <agent> <event>` onto the function that interprets that
// agent's hook callbacks as a Tallpine activity state.
//
// The hidden `tallpine hooks` CLI command dispatches a live callback through it. Every
// adapter that installs `tallpine hooks <tok>` callbacks must have a deriver
// registered here — otherwise the adapter writes callbacks that nothing on the
// receiving side understands, so its activity is silently never reported.
package activitydispatch

import (
	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/codex"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/deepseekharness"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/fake"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/kiro"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// DeriveFunc maps a native agent hook event and its raw stdin payload onto a Tallpine
// activity state. ok=false means the event carries no activity signal.
type DeriveFunc func(event string, payload []byte) (domain.ActivityState, bool)

// Derivers maps the agent token in `tallpine hooks <agent> <event>` to its deriver.
var Derivers = map[string]DeriveFunc{
	// Adapters that parse hook payloads for finer-grained state keep their own
	// deriver; the rest share the name-only StandardDeriveActivityState.
	"claude-code":      claudecode.DeriveActivityState,
	"codex":            codex.DeriveActivityState,
	"kiro":             kiro.DeriveActivityState,
	"deepseek-harness": deepseekharness.DeriveActivityState,
	"fake":             fake.DeriveActivityState,
}

// SignalCoverage describes how much of a harness lifecycle Tallpine can observe.
// Partial coverage can report useful transitions but cannot prove that silence
// means a broken pipeline. Complete coverage is eligible for the no_signal
// watchdog because the harness is expected to report promptly after launch or
// prompt submission.
type SignalCoverage uint8

const (
	// SignalCoverageNone means the harness has no activity callback pipeline.
	SignalCoverageNone SignalCoverage = iota
	// SignalCoveragePartial means the harness emits only a subset of lifecycle
	// transitions, such as a completion-only notification.
	SignalCoveragePartial
	// SignalCoverageComplete means the harness emits enough lifecycle callbacks
	// for prolonged initial silence to indicate a broken pipeline.
	SignalCoverageComplete
)

// signalCoverageOverrides records harnesses whose callback coverage cannot be
// inferred from a same-named Derivers entry. None of the shipped harnesses need
// an override today.
var signalCoverageOverrides = map[domain.AgentHarness]SignalCoverage{}

// CoverageForHarness returns the activity-signal coverage for a selectable
// harness. Same-named callback pipelines are complete by default; exceptional
// aliases and partial pipelines are declared above.
func CoverageForHarness(h domain.AgentHarness) SignalCoverage {
	if coverage, ok := signalCoverageOverrides[h]; ok {
		return coverage
	}
	if _, ok := Derivers[string(h)]; ok {
		return SignalCoverageComplete
	}
	return SignalCoverageNone
}

// Derive looks up the deriver for an agent token and applies it. ok=false when
// the token has no registered deriver or the event carries no activity signal —
// the caller reports nothing in either case.
func Derive(agent, event string, payload []byte) (domain.ActivityState, bool) {
	derive, found := Derivers[agent]
	if !found {
		return "", false
	}
	return derive(event, payload)
}

// SupportsHarness reports whether a harness has any activity callback pipeline,
// including partial coverage and compatibility aliases.
func SupportsHarness(h domain.AgentHarness) bool {
	return CoverageForHarness(h) != SignalCoverageNone
}

// FullySupportsHarness reports whether a harness has complete activity signal
// coverage. Status derivation uses this narrower predicate for the no_signal
// watchdog so a partial pipeline is not penalized for callbacks it cannot emit.
func FullySupportsHarness(h domain.AgentHarness) bool {
	return CoverageForHarness(h) == SignalCoverageComplete
}
