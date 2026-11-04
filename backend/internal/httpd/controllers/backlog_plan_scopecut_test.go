package controllers

import (
	"encoding/json"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestPlanView_CarriesAssumptionScopeCut(t *testing.T) {
	v := planView(domain.BacklogPlan{Assumptions: []domain.PlanAssumption{
		{ID: "a1", Text: "Windows later", ScopeCut: true},
		{ID: "a2", Text: "Uses the store"},
	}})
	if !v.Assumptions[0].ScopeCut || v.Assumptions[1].ScopeCut {
		t.Fatalf("scopeCut not mapped: %+v", v.Assumptions)
	}
	raw, _ := json.Marshal(v.Assumptions[1])
	if strings.Contains(string(raw), "scopeCut") {
		t.Fatalf("unset flag should be omitted: %s", raw)
	}
}
