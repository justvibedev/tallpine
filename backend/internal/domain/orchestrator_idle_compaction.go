package domain

import (
	"fmt"
	"time"
)

// Idle compaction (B204): Tallpine compacts an orchestrator between tasks, when
// nothing is in flight, so the next task starts with a small context. It keeps
// the session and its chat; the provider's own window (B193) still caps it
// mid-task.
const (
	// OrchestratorIdleCompactDefault is the context size, as a percent of the
	// orchestrator's effective compaction window (the busy window below, not the
	// model's: 30% of 120k is 36k), above which an idle
	// orchestrator compacts.
	OrchestratorIdleCompactDefault = "30%"
	// IdleCompactWait is how long an orchestrator must have been quiet.
	IdleCompactWait = 10 * time.Minute
	// IdleCompactStalledWorker is how long a worker with an open task may go
	// without activity before it no longer holds idle compaction back.
	IdleCompactStalledWorker = 2 * time.Hour
)

// What an idle orchestrator past its threshold does (orchestrator.idleAction).
// Handoff (B374) replaces it with a fresh orchestrator that rebuilds its state
// from the daemon, instead of summarising the old chat; it is opt-in.
const (
	IdleActionCompact = "compact"
	IdleActionHandoff = "handoff"
)

// ValidIdleCompactAt reports whether v is a valid orchestrator.idleCompactAt:
// empty (the default), AutoCompactOff, a token preset (200k, 1m) or a whole
// percent of the effective window.
func ValidIdleCompactAt(v string) bool {
	return v == AutoCompactOff || ValidAutoCompactWindow(v)
}

// IdleCompactThreshold is the context size in tokens above which an idle
// orchestrator compacts, given the project's idleCompactAt and the session's
// effective compaction window in tokens. 0 means never: the setting is off, a
// percent has no known window, or the threshold is not below the window, where
// the provider compacts on its own anyway.
func IdleCompactThreshold(setting string, window int64) int64 {
	switch setting {
	case AutoCompactOff:
		return 0
	case "":
		setting = OrchestratorIdleCompactDefault
	}
	var threshold int64
	if tokens, ok := ParseAutoCompactTokens(setting); ok {
		threshold = int64(tokens)
	} else if percent, ok := ParseAutoCompactPercent(setting); ok && window > 0 {
		threshold = window * int64(percent) / 100
	}
	if threshold <= 0 || (window > 0 && threshold >= window) {
		return 0
	}
	return threshold
}

// IdleCompactFacts are what decides one orchestrator's idle compaction.
type IdleCompactFacts struct {
	// Busy covers a turn, queued messages, a pending compaction or approval.
	Busy bool
	// IdleFor is how long the orchestrator has been quiet.
	IdleFor time.Duration
	// ContextUsed is its current context size in tokens.
	ContextUsed int64
	// Threshold is IdleCompactThreshold for it; 0 disables idle compaction.
	Threshold int64
	// CompactedAtThisActivity reports an idle compaction already made since its
	// last activity, so a compaction that did not shrink it is not repeated.
	CompactedAtThisActivity bool
	// BackgroundTasks counts the provider's background tasks still running.
	BackgroundTasks int
	// PendingReports counts undelivered reports for it (pending or claimed).
	PendingReports int
	// InFlightWorkers lists routed live workers with an open task (latest
	// report not done) and activity within IdleCompactStalledWorker.
	InFlightWorkers []SessionID
	// RunningReviews counts native reviews running on its workers' PRs.
	RunningReviews int
	// Handoff reports the project chose IdleActionHandoff, so the sweep hands
	// the orchestrator off instead of compacting it.
	Handoff bool
}

// IdleCompactSkipReason is why an orchestrator does not compact now, or "" when
// it should.
func (f IdleCompactFacts) IdleCompactSkipReason() string {
	switch {
	case f.Threshold <= 0:
		return "off"
	case f.Busy:
		return "busy"
	case f.IdleFor < IdleCompactWait:
		return "recently active"
	case f.ContextUsed < f.Threshold:
		return "below threshold"
	case f.CompactedAtThisActivity:
		return "already compacted"
	case f.BackgroundTasks > 0:
		return "background tasks running"
	case f.PendingReports > 0:
		return "reports pending"
	case len(f.InFlightWorkers) > 0:
		return "workers in flight"
	case f.RunningReviews > 0:
		return "reviews running"
	}
	return ""
}

// IdleCompactInstructions is what the provider is asked to keep when it
// compacts an idle orchestrator, where it accepts instructions (Claude Code's
// /compact).
const IdleCompactInstructions = "This is an idle compaction between tasks. Keep: " +
	"a table of in-flight PRs and workers (session ids, PR numbers and URLs, head SHAs, review and merge state); " +
	"the owner's open to-dos and pending decisions; " +
	"the owner's standing preferences and instructions stated in this chat; " +
	"a line telling the resumed orchestrator to run `tallpine orchestrator intent ls` first, and a list of owner decisions from this chat that are not yet recorded as intents so it can run `tallpine orchestrator intent add` for each. " +
	"the conclusions of any research you did in this chat (findings, file paths and decisions, not the raw files you read); " +
	"Drop finished tasks' details, tool output and resolved threads."

// validateIdle checks the idle fields of the role overrides: only the
// orchestrator role sets them (B204, B374).
func (c ProjectConfig) validateIdle() error {
	if c.Planner.AutoCompactWindow != "" || c.Planner.IdleCompactAt != "" {
		return fmt.Errorf("planner: only the orchestrator role sets compaction")
	}
	if c.Worker.IdleAction != "" || c.Planner.IdleAction != "" {
		return fmt.Errorf("only the orchestrator role sets idleAction")
	}
	if a := c.Orchestrator.IdleAction; a != "" && a != IdleActionCompact && a != IdleActionHandoff {
		return fmt.Errorf("orchestrator.idleAction: %q is not %s or %s", a, IdleActionCompact, IdleActionHandoff)
	}
	if c.Worker.IdleCompactAt != "" {
		return fmt.Errorf("worker.idleCompactAt: only the orchestrator role sets an idle compaction threshold")
	}
	if w := c.Orchestrator.IdleCompactAt; !ValidIdleCompactAt(w) {
		return fmt.Errorf("orchestrator.idleCompactAt: %q is not a size from 50k to 1m, a percent from %d%% to %d%%, or %s", w, AutoCompactPercentMin, AutoCompactPercentMax, AutoCompactOff)
	}
	return nil
}
