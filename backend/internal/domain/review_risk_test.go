package domain

import "testing"

func TestReviewRiskResolveDefaults(t *testing.T) {
	paths, harness, model, effort := ReviewRiskConfig{}.Resolve()
	if harness != ReviewerClaudeCode || model != "claude-opus-5-5" || effort != "high" || len(paths) != 1 || paths[0] != "**/migrations/**" {
		t.Fatalf("defaults = %v %q %q %q", paths, harness, model, effort)
	}
}

func TestReviewRiskResolveExplicitChoices(t *testing.T) {
	for _, tc := range []struct {
		name          string
		in            ReviewRiskConfig
		harness       ReviewerHarness
		model, effort string
	}{
		{"model only keeps the agent default effort", ReviewRiskConfig{Model: "claude-fable-5-1"}, ReviewerClaudeCode, "claude-fable-5-1", ""},
		{"codex without model uses the agent's own model", ReviewRiskConfig{Harness: ReviewerCodex}, ReviewerCodex, "", ""},
		{"codex with model and effort", ReviewRiskConfig{Harness: ReviewerCodex, Model: "m", Effort: "low"}, ReviewerCodex, "m", "low"},
		{"claude-code without model gets the default model", ReviewRiskConfig{Harness: ReviewerClaudeCode}, ReviewerClaudeCode, "claude-opus-5-5", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, h, m, e := tc.in.Resolve()
			if h != tc.harness || m != tc.model || e != tc.effort {
				t.Fatalf("got %q %q %q, want %q %q %q", h, m, e, tc.harness, tc.model, tc.effort)
			}
		})
	}
}

func TestReviewRiskValidate(t *testing.T) {
	for name, tc := range map[string]struct {
		in ReviewRiskConfig
		ok bool
	}{
		"zero":            {ReviewRiskConfig{}, true},
		"full":            {ReviewRiskConfig{Harness: ReviewerCodex, Model: "m", Effort: "high", Paths: []string{"a/**"}}, true},
		"unknown harness": {ReviewRiskConfig{Harness: "nope"}, false},
		"unknown effort":  {ReviewRiskConfig{Effort: "extreme"}, false},
		"blank path":      {ReviewRiskConfig{Paths: []string{" "}}, false},
	} {
		if err := tc.in.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", name, err, tc.ok)
		}
	}
}
