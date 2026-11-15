package domain

import (
	"fmt"
	"slices"
	"time"
)

// Orchestrator research mode (B413): an orchestrator that must examine a large
// body of code or documents itself switches it on, which raises its compaction
// window so the material is not compacted away mid-analysis. Plain
// coordination keeps the early window (OrchestratorAutoCompactDefault).
const (
	// OrchestratorResearchCompactDefault is the compaction window of an
	// orchestrator in research mode when the project sets none.
	OrchestratorResearchCompactDefault = "250k"
	// ResearchQuietExpiry is how long a research orchestrator may go without a
	// turn before its research mode ends.
	ResearchQuietExpiry = 60 * time.Minute
)

// ResearchCompactChoice is the compaction choice for an orchestrator in
// research mode: the project's orchestrator.researchCompactWindow, else
// OrchestratorResearchCompactDefault. ok is false when research mode is off
// for it: the global Settings toggle is off or the project set AutoCompactOff.
func ResearchCompactChoice(project ProjectConfig, globalEnabled bool) (choice string, ok bool) {
	if !globalEnabled {
		return "", false
	}
	switch own := project.Orchestrator.ResearchCompactWindow; own {
	case AutoCompactOff:
		return "", false
	case "":
		return OrchestratorResearchCompactDefault, true
	default:
		return own, true
	}
}

// ValidResearchCompactWindow reports whether v is a valid
// orchestrator.researchCompactWindow: empty (the default), AutoCompactOff, a
// token preset or a whole percent of the model window.
func ValidResearchCompactWindow(v string) bool {
	return v == AutoCompactOff || ValidAutoCompactWindow(v)
}

// validateResearch checks orchestrator.researchCompactWindow: only the
// orchestrator role sets it.
func (c ProjectConfig) validateResearch() error {
	if c.Worker.ResearchCompactWindow != "" || c.Planner.ResearchCompactWindow != "" {
		return fmt.Errorf("only the orchestrator role sets researchCompactWindow")
	}
	if w := c.Orchestrator.ResearchCompactWindow; !ValidResearchCompactWindow(w) {
		return fmt.Errorf("orchestrator.researchCompactWindow: %q is not a size from 50k to 1m, a percent from %d%% to %d%%, or %s", w, AutoCompactPercentMin, AutoCompactPercentMax, AutoCompactOff)
	}
	return nil
}

// OrchestratorWindowLadder is the rungs an orchestrator's research window
// steps through (B736): `research on --window` takes one of them, and an
// orchestrator whose turn stopped on compaction thrash moves to the next one
// above its current window.
var OrchestratorWindowLadder = []string{"250k", "400k", "1m"}

// ValidResearchWindow reports whether v is a size `research on --window`
// accepts: a rung of OrchestratorWindowLadder.
func ValidResearchWindow(v string) bool {
	return slices.Contains(OrchestratorWindowLadder, v)
}

// ResearchWindowFits reports whether the research window fits a model whose
// context window is modelWindow tokens. A window the size of the model's own
// fits: the agent then compacts at its own default. An unknown model window
// (0) fits, as ResolveAutoCompactWindow keeps an explicit size it cannot check.
func ResearchWindowFits(window string, modelWindow int64) bool {
	tokens, ok := ParseAutoCompactTokens(window)
	return ok && (modelWindow <= 0 || int64(tokens) <= modelWindow)
}

// NextOrchestratorWindow is the rung an orchestrator steps up to after a
// compaction thrash: the first one strictly above its current window, in
// tokens. ok is false at the agent's own default (current 0, the largest),
// past the top rung, when the next rung does not fit the model, or when the
// model window is unknown.
func NextOrchestratorWindow(current int, modelWindow int64) (string, bool) {
	if current <= 0 || modelWindow <= 0 {
		return "", false
	}
	for _, rung := range OrchestratorWindowLadder {
		tokens, _ := ParseAutoCompactTokens(rung)
		if tokens <= current {
			continue
		}
		if !ResearchWindowFits(rung, modelWindow) {
			return "", false
		}
		return rung, true
	}
	return "", false
}

// SessionResearch is an orchestrator's persisted research mode (B736), so a
// daemon restart keeps it. Every end of research mode clears it.
type SessionResearch struct {
	// Window is the research window chosen for this session (a ladder rung);
	// empty follows the project's orchestrator.researchCompactWindow.
	Window string
	// Since is when research mode was switched on.
	Since time.Time
	// LastTurnAt is when the orchestrator last finished a turn in research
	// mode; ResearchQuietExpiry runs from it.
	LastTurnAt time.Time
}
