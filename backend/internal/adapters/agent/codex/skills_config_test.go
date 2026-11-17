package codex

import (
	"context"
	"slices"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Spike (B745): codex 0.169 app-server skills/list reports a skill disabled
// when -c skills.config=[{name="beta",enabled=false}] names it, and leaves
// the others enabled. The role's off skills reach Codex that way.
func TestGetLaunchCommandSwitchesOffRoleSkills(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}
	launch := domain.SkillLaunch{Off: []string{"beta", `odd"name`}}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{Config: ports.AgentConfig{
		Launch: domain.AgentLaunchOptions{Skills: launch.Encode()},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := `skills.config=[{name='beta',enabled=false},{name='odd"name',enabled=false}]`
	i := slices.Index(cmd, want)
	if i < 1 || cmd[i-1] != "-c" {
		t.Fatalf("argv lacks %s: %#v", want, cmd)
	}
	plain, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range plain {
		if len(a) > 13 && a[:13] == "skills.config" {
			t.Fatalf("skills.config set with no switched-off skill: %#v", plain)
		}
	}
}
