package accountpool

import (
	"encoding/json"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func decide(settings map[string]string) Outcome {
	s := agentpolicy.Settings{}
	for k, v := range settings {
		s[k] = json.RawMessage(v)
	}
	return Rule{}.Decide(agentpolicy.Input{}, s).(Outcome)
}

func TestDefaultsChangeNothing(t *testing.T) {
	o := decide(nil)
	if o.Placement() != domain.AccountPlacementDefault || o.Failover() {
		t.Fatalf("defaults = %q/%t, want default/off", o.Placement(), o.Failover())
	}
	if z := Of(agentpolicy.Decision{}); z.Placement() != domain.AccountPlacementDefault || z.Failover() {
		t.Fatalf("zero decision = %q/%t", z.Placement(), z.Failover())
	}
}

func TestSettingsAreCarried(t *testing.T) {
	o := decide(map[string]string{FieldPlacement: `"least-used"`, FieldFailover: `true`})
	if o.Placement() != domain.AccountPlacementLeastUsed || !o.Failover() {
		t.Fatalf("outcome = %q/%t", o.Placement(), o.Failover())
	}
}

func TestUnknownPlacementFallsBackToDefault(t *testing.T) {
	if got := decide(map[string]string{FieldPlacement: `"fastest"`}).Placement(); got != domain.AccountPlacementDefault {
		t.Fatalf("placement = %q", got)
	}
}
