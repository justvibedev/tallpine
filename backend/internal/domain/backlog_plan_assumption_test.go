package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPlanAssumption_ScopeCutRoundTrip(t *testing.T) {
	raw, err := json.Marshal(PlanAssumption{ID: "a1", Text: "Windows is deferred", ScopeCut: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"scopeCut":true`) {
		t.Fatalf("scopeCut not serialised: %s", raw)
	}
	var got PlanAssumption
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !got.ScopeCut || got.ID != "a1" {
		t.Fatalf("round trip lost the flag: %+v", got)
	}
}

func TestPlanAssumption_MissingScopeCutReadsFalse(t *testing.T) {
	var got PlanAssumption
	if err := json.Unmarshal([]byte(`{"id":"a1","text":"Uses the store"}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.ScopeCut {
		t.Fatal("an old plan without the field must read scopeCut=false")
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "scopeCut") {
		t.Fatalf("false flag should be omitted: %s", raw)
	}
	if err := ValidatePlanQuestions(nil, []PlanAssumption{{ID: "a1", Text: "x", ScopeCut: true}}); err != nil {
		t.Fatalf("validation must accept the flag: %v", err)
	}
}
