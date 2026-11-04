// Package registry is the single source of truth for the agent adapters the
// daemon ships. The daemon wires sessions through it, so adding a harness is a
// single edit to Constructors rather than a list maintained in several places.
package registry

import (
	"fmt"

	"tallpine.eu/tallpine/backend/internal/adapters"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/codex"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/deepseekharness"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/kiro"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Constructors returns a fresh instance of every agent adapter the daemon
// ships, in a stable registration order. Adding a new harness means adding its
// constructor here (and a domain.AgentHarness constant) — the one edit the
// daemon picks up.
func Constructors() []adapters.Adapter {
	return []adapters.Adapter{
		claudecode.New(),
		codex.New(),
		kiro.New(),
		deepseekharness.New(),
	}
}

// Build returns a registry populated with the shipped agent adapters, keyed by
// manifest id. Registration only fails on an empty/duplicate id — a programmer
// error, not a runtime condition.
func Build() (*adapters.Registry, error) {
	reg := adapters.NewRegistry()
	for _, a := range Constructors() {
		if err := reg.Register(a); err != nil {
			return nil, fmt.Errorf("register agent adapter %q: %w", a.Manifest().ID, err)
		}
	}
	return reg, nil
}

// Harnessed returns every shipped adapter that drives an agent, paired with its
// harness, in Constructors() order. An adapter that does not implement
// ports.Agent is skipped.
func Harnessed() []ports.HarnessAgent {
	cons := Constructors()
	out := make([]ports.HarnessAgent, 0, len(cons))
	for _, a := range cons {
		agent, ok := a.(ports.Agent)
		if !ok {
			continue
		}
		out = append(out, ports.HarnessAgent{
			Harness: domain.AgentHarness(a.Manifest().ID),
			Name:    a.Manifest().Name,
			Agent:   agent,
		})
	}
	return out
}
