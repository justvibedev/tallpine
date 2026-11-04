package domain

import (
	"fmt"
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
