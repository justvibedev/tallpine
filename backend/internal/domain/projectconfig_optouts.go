package domain

import "time"

// ChatHibernationDisabled reports whether the project opted out of idle Chat
// hibernation.
func (c ProjectConfig) ChatHibernationDisabled() bool {
	return c.ChatHibernation != nil && c.ChatHibernation.Disabled
}

// ChatHibernationConfig is the project-level opt-out for idle Chat
// hibernation. The app-wide setting still applies when this is unset.
type ChatHibernationConfig struct {
	// Disabled keeps every Chat session of this project awake however long
	// it is idle.
	Disabled bool `json:"disabled,omitempty"`
}

// ContainerReapConfig is the project-level opt-out for #2652's Docker
// container reaping on session terminal state.
type ContainerReapConfig struct {
	// Disabled turns off container reaping for every session in this project.
	// Per-container sparing (tallpine.spare=true) is unaffected either way.
	Disabled bool `json:"disabled,omitempty"`
}

// OrchestratorIdleNudgeAfter is how long an orchestrator must be idle before
// the idle nudge (ProjectConfig.OrchestratorIdleNudge) fires.
const OrchestratorIdleNudgeAfter = 15 * time.Minute

// DefaultContextBudgetTokens is the instruction-file budget used when
// ProjectConfig.ContextBudgetTokens is unset.
const DefaultContextBudgetTokens = 15000

// DefaultContextMemoryBudgetTokens is the memory-index budget used when
// ProjectConfig.ContextMemoryBudgetTokens is unset.
const DefaultContextMemoryBudgetTokens = 8000
