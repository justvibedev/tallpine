package repoagentconfig

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/ruletest"
)

func newEngine(t *testing.T, managed string) *agentpolicy.Engine {
	t.Helper()
	return ruletest.Engine(t, Rule{}, managed)
}

func enabled(e *agentpolicy.Engine) bool {
	return Of(e.Decide(context.Background(), agentpolicy.SessionRef{Project: "p"})).Enabled()
}

func TestZeroDecisionLoadsRepoConfig(t *testing.T) {
	if !Of(agentpolicy.Decision{}).Enabled() {
		t.Fatal("the zero decision must load repo config")
	}
}

func TestOnByDefault(t *testing.T) {
	if !enabled(newEngine(t, "")) {
		t.Fatal("repo agent config must load by default")
	}
}

func TestProjectTurnsOff(t *testing.T) {
	e := newEngine(t, "")
	ruletest.SetProjectEnabled(e, Name, false)
	if enabled(e) {
		t.Fatal("project setting did not turn the rule off")
	}
}

func TestManagedFileForcesOff(t *testing.T) {
	e := newEngine(t, `{"schemaVersion":2,"rules":{"repoAgentConfig":{"enabled":false}}}`)
	ruletest.SetProjectEnabled(e, Name, true)
	if enabled(e) {
		t.Fatal("managed policy must force repo config off over a project opt-in")
	}
}
