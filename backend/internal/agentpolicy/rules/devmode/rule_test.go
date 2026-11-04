package devmode

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

var optInRule = ruletest.OptInRule{Name: Name, Label: "dev mode", NewEngine: newEngine, Enabled: enabled}

func TestOffByDefault(t *testing.T)         { optInRule.OffByDefault(t) }
func TestManagedFileForcesOff(t *testing.T) { optInRule.ManagedFileForcesOff(t) }

// The field is not project-overridable: a project can never turn Dev mode on.
func TestFieldIsNeverProjectOverridable(t *testing.T) {
	for _, f := range (Rule{}).Schema().Fields {
		if f.ProjectOverride != agentpolicy.ProjectNever {
			t.Fatalf("%s project override = %q, want never", f.Name, f.ProjectOverride)
		}
	}
}
