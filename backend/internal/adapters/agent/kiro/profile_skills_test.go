package kiro

import (
	"encoding/json"
	"slices"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// A Kiro custom agent loads only the skills its resources list (kiro-cli 3.1
// validates skill:// entries), so the role's enabled skills are the list.
func TestKiroProfileListsOnlyEnabledSkills(t *testing.T) {
	resources := func(launch domain.SkillLaunch) []string {
		top := map[string]json.RawMessage{}
		if err := setKiroAgentDefaults(top, "", ports.AgentConfig{Launch: domain.AgentLaunchOptions{Skills: launch.Encode()}}); err != nil {
			t.Fatal(err)
		}
		var out []string
		if err := json.Unmarshal(top["resources"], &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	want := append(slices.Clone(kiroSteeringResources), "skill://.claude/skills/a/SKILL.md")
	if got := resources(domain.SkillLaunch{Resources: []string{"skill://.claude/skills/a/SKILL.md"}}); !slices.Equal(got, want) {
		t.Fatalf("resources = %v, want %v", got, want)
	}
	if got := resources(domain.SkillLaunch{}); !slices.Equal(got, kiroSteeringResources) {
		t.Fatalf("no skills: resources = %v", got)
	}
}
