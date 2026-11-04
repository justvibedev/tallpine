package crossprojectorchestration

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

var optInRule = ruletest.OptInRule{Name: Name, Label: "cross-project orchestration", NewEngine: newEngine, Enabled: enabled}

func TestOffByDefault(t *testing.T)         { optInRule.OffByDefault(t) }
func TestProjectOptsIn(t *testing.T)        { optInRule.ProjectOptsIn(t) }
func TestManagedFileForcesOff(t *testing.T) { optInRule.ManagedFileForcesOff(t) }
