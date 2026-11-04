package ports

import "tallpine.eu/tallpine/backend/internal/domain"

// HarnessAgent pairs a session harness with the adapter that drives it. The
// harness is the adapter's manifest id, which is also the domain.AgentHarness
// value a session carries and the `--harness` flag users pass.
type HarnessAgent struct {
	Harness domain.AgentHarness
	// Name is the adapter's display name (its manifest name).
	Name  string
	Agent Agent
}

// AgentRegistry lists the shipped agent adapters. Each call returns fresh
// adapter instances so a caller can re-probe without sharing state.
type AgentRegistry func() []HarnessAgent
