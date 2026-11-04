package cli

import (
	"encoding/json"
	"testing"
)

// An explicit false must survive a set-config round trip: nil is "never set",
// so dropping false would lose a project's recorded opt-out.
func TestProjectConfigRoundTripsReviewDefaults(t *testing.T) {
	in := `{"autoReview":false,"terminateOnPrMerge":false,"reviewRisk":{"paths":["db/**"],"harness":"codex","model":"m","effort":"high","firstReviewOnly":true}}`
	var cfg projectConfig
	if err := json.Unmarshal([]byte(in), &cfg); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if v, ok := got["autoReview"]; !ok || v != false {
		t.Errorf("autoReview dropped: %s", out)
	}
	if v, ok := got["terminateOnPrMerge"]; !ok || v != false {
		t.Errorf("terminateOnPrMerge dropped: %s", out)
	}
	risk, _ := got["reviewRisk"].(map[string]any)
	if risk["harness"] != "codex" || risk["model"] != "m" || risk["effort"] != "high" || risk["firstReviewOnly"] != true {
		t.Errorf("reviewRisk lost the stronger reviewer: %s", out)
	}
	var unset map[string]any
	out, _ = json.Marshal(projectConfig{})
	_ = json.Unmarshal(out, &unset)
	if _, ok := unset["autoReview"]; ok {
		t.Errorf("unset autoReview must stay omitted: %s", out)
	}
}

// The replace path (--config-json) must put explicit false in the PUT body.
func TestProjectSetConfigJSONSendsExplicitFalseReviewDefaults(t *testing.T) {
	got, _, err := runSetConfig(t, mergeBaseConfig, "--config-json", `{"autoReview":false,"terminateOnPrMerge":false}`)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := got["autoReview"]; !ok || v != false {
		t.Errorf("autoReview = %v (present %v), want false", v, ok)
	}
	if v, ok := got["terminateOnPrMerge"]; !ok || v != false {
		t.Errorf("terminateOnPrMerge = %v (present %v), want false", v, ok)
	}
}
