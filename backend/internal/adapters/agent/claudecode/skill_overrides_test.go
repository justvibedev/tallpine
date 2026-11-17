package claudecode

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Spike (B745): Claude Code 2.1 hides a skill named in skillOverrides with
// "off" from the model and from /name, and reads --settings among its
// highest-priority sources. The role's off skills reach it that way.
func TestLaunchSettingsHideSwitchedOffSkills(t *testing.T) {
	launch := domain.SkillLaunch{Off: []string{"design", "review-helper"}}
	got := launchSettings(t, domain.AgentLaunchOptions{Skills: launch.Encode()})
	overrides, _ := got["skillOverrides"].(map[string]any)
	if len(overrides) != 2 || overrides["design"] != "off" || overrides["review-helper"] != "off" {
		t.Fatalf("settings = %v", got)
	}
	if _, ok := launchSettings(t, domain.AgentLaunchOptions{})["skillOverrides"]; ok {
		t.Fatal("skillOverrides written with no switched-off skill")
	}
}
