package domain

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestCodeIntelApplies(t *testing.T) {
	if !CodeIntelApplies(KindWorker, HarnessClaudeCode) {
		t.Fatal("claude worker should apply")
	}
	if CodeIntelApplies(KindOrchestrator, HarnessClaudeCode) || CodeIntelApplies(KindWorker, HarnessCodex) {
		t.Fatal("orchestrators and other agents must not apply")
	}
}

func TestWithCodeIntelDenyKeepsExistingSettings(t *testing.T) {
	var got struct {
		Memory bool `json:"autoMemoryEnabled"`
		Perms  struct {
			Deny []string `json:"deny"`
			Mode string   `json:"defaultMode"`
		} `json:"permissions"`
	}
	in := `{"autoMemoryEnabled":false,"permissions":{"defaultMode":"auto","deny":["Read(//x)","Grep"]}}`
	if err := json.Unmarshal([]byte(WithCodeIntelDeny(in)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Perms.Mode != "auto" || !slices.Contains(got.Perms.Deny, "Read(//x)") || !slices.Contains(got.Perms.Deny, "Bash(rtk rg *)") {
		t.Fatalf("settings = %+v", got)
	}
	if n := len(got.Perms.Deny); n != len(CodeIntelDeny)+1 {
		t.Fatalf("Grep duplicated: %v", got.Perms.Deny)
	}
	if WithCodeIntelDeny("") == "" {
		t.Fatal("empty settings should still get the deny list")
	}
}
