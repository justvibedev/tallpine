package domain

import (
	"fmt"
	"slices"
)

// A session's baseline is the context its first model call already carries:
// system prompt, tools, CLAUDE.md, memory and the task. A compaction window
// close to it leaves no room to work, so the agent compacts again right after
// every compaction (B733). Tallpine measures it per project, session kind and
// harness from the usage ledger, at launch only.
const (
	// AutoCompactBaselineDefault is the baseline used until there are
	// AutoCompactBaselineMinSamples measured sessions.
	AutoCompactBaselineDefault int64 = 70_000
	// AutoCompactBaselineMinSamples is how many sessions a measured baseline needs.
	AutoCompactBaselineMinSamples = 3
	// AutoCompactBaselineSessions is how many of the newest sessions are read.
	AutoCompactBaselineSessions = 10
	// AutoCompactWorkRoom is the room above the baseline every resolved
	// compaction window keeps: window = max(choice, baseline + this).
	AutoCompactWorkRoom int64 = 100_000
	// IdleCompactBaselineRoom is the room above the baseline an orchestrator's
	// idle-compaction threshold keeps, so an idle compaction does not fire on a
	// context that is mostly its own baseline.
	IdleCompactBaselineRoom int64 = 30_000
	// ClaudeAutoCompactReserve is what Claude Code keeps below its compaction
	// window (output reserve plus its buffer): it compacts at window minus this.
	ClaudeAutoCompactReserve = 33_000
	// AutoCompactSummaryTokens is roughly what a compaction summary adds back.
	AutoCompactSummaryTokens int64 = 8_000
	// AutoCompactRoomWarn is the room under which spawn warns that the baseline
	// leaves too little space on the model to work between compactions.
	AutoCompactRoomWarn int64 = 60_000
)

// AutoCompactBaseline is the median of the measured first-call input sizes,
// or AutoCompactBaselineDefault with fewer than AutoCompactBaselineMinSamples.
// An even count takes the mean of the two middle values.
func AutoCompactBaseline(samples []int64) int64 {
	if len(samples) < AutoCompactBaselineMinSamples {
		return AutoCompactBaselineDefault
	}
	sorted := slices.Clone(samples)
	slices.Sort(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// AutoCompactBaselineKind is the kind a session's baseline is measured under:
// orchestrators (and lanes) apart, every other role (worker, planner,
// reviewer) as a worker.
func AutoCompactBaselineKind(kind SessionKind) SessionKind {
	if kind == KindOrchestrator {
		return KindOrchestrator
	}
	return KindWorker
}

// AutoCompactTrigger is the context size at which an agent with the resolved
// window compacts, or 0 when Tallpine passes no window (agent default). Claude
// Code compacts ClaudeAutoCompactReserve below its window; Codex's
// model_auto_compact_token_limit is the trigger itself.
func AutoCompactTrigger(harness AgentHarness, window int) int {
	if window <= 0 {
		return 0
	}
	if harness == HarnessCodex {
		return window
	}
	return max(window-ClaudeAutoCompactReserve, 0)
}

// AutoCompactRoom is the context left to work between compactions on a model:
// its window less the compaction reserve, the baseline and a summary. It is
// meaningful only for a known model window. Claude Code's reserve is used for
// every agent: Codex keeps a reserve of its own, and this only gates a warning.
func AutoCompactRoom(modelWindow, baseline int64) int64 {
	return modelWindow - ClaudeAutoCompactReserve - baseline - AutoCompactSummaryTokens
}

// AutoCompactRoomTight reports a known model window whose room for the
// baseline is under AutoCompactRoomWarn; spawn warns, and does not refuse.
func AutoCompactRoomTight(modelWindow, baseline int64) bool {
	return modelWindow > 0 && baseline > 0 && AutoCompactRoom(modelWindow, baseline) < AutoCompactRoomWarn
}

// AutoCompactView is what a session's compaction resolves to, for session get,
// spawn output and the desktop readout: the launch baseline, the window
// Tallpine passes (0 is the agent's own default), where the agent compacts,
// and the model's window (0 when unknown).
type AutoCompactView struct {
	Baseline    int64 `json:"baseline"`
	Window      int   `json:"window"`
	Trigger     int   `json:"trigger"`
	ModelWindow int64 `json:"modelWindow"`
	// RoomTight reports that the baseline leaves under AutoCompactRoomWarn of
	// room on the model (AutoCompactRoomTight).
	RoomTight bool `json:"roomTight"`
}

// NewAutoCompactView resolves a launch's compaction options for display.
func NewAutoCompactView(harness AgentHarness, options AgentLaunchOptions) AutoCompactView {
	window := ResolveAutoCompactWindow(options.AutoCompactWindow, options.ModelContextWindow, options.AutoCompactBaseline)
	return AutoCompactView{
		Baseline:    options.AutoCompactBaseline,
		Window:      window,
		Trigger:     AutoCompactTrigger(harness, window),
		ModelWindow: options.ModelContextWindow,
		RoomTight:   AutoCompactRoomTight(options.ModelContextWindow, options.AutoCompactBaseline),
	}
}

// RoomWarning is the spawn warning for a tight view, "" otherwise.
func (v AutoCompactView) RoomWarning() string {
	if !v.RoomTight {
		return ""
	}
	return fmt.Sprintf("context baseline %dk leaves under %dk of room on the model's %dk window, so the agent may compact repeatedly; use a larger-context model or trim the project instructions",
		v.Baseline/1000, AutoCompactRoomWarn/1000, v.ModelWindow/1000)
}
