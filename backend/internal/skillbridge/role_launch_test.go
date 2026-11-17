package skillbridge

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// roleFixture is a project with one repo skill, one account skill whose
// SKILL.md declares another name, a Tallpine skill and an enabled plugin.
func roleFixture(t *testing.T) Inventory {
	t.Helper()
	ws, home, data := t.TempDir(), t.TempDir(), t.TempDir()
	skill(t, filepath.Join(ws, ".claude", "skills"), "repo-a", "repo")
	write(t, filepath.Join(home, ".claude", "skills", "user-b", "SKILL.md"), "---\nname: user-b-decl\ndescription: user\n---\n")
	skill(t, filepath.Join(data, "skills"), "using-tallpine", "tallpine")
	write(t, filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"p@m":true}}`)
	return Discover(Options{Workspace: ws, Home: home, DataDir: data})
}

// Reviewers get nothing unless switched on (B740 q1): every account skill is
// hidden and every account plugin is off, while workers keep them.
func TestRoleLaunchReviewerDefaultsOff(t *testing.T) {
	inv := roleFixture(t)
	launch, plugins := RoleLaunch(inv, domain.ProjectConfig{}, nil, domain.RoleReviewer, "claude-code")
	if !reflect.DeepEqual(launch.Off, []string{"user-b", "user-b-decl"}) || plugins != `{"p@m":false}` {
		t.Fatalf("reviewer launch = %+v plugins %s", launch, plugins)
	}
	launch, plugins = RoleLaunch(inv, domain.ProjectConfig{}, nil, "worker", "claude-code")
	if len(launch.Off) != 0 || plugins != "" {
		t.Fatalf("worker defaults changed the launch: %+v plugins %s", launch, plugins)
	}
	cfg := domain.ProjectConfig{
		SkillRoles:    domain.SkillRoles{"reviewer": {"user-b": true}, "worker": {"repo-a": false}},
		ClaudePlugins: map[string]map[string]bool{"reviewer": {"p@m": true}},
	}
	launch, plugins = RoleLaunch(inv, cfg, nil, domain.RoleReviewer, "claude-code")
	if len(launch.Off) != 0 || plugins != `{"p@m":true}` {
		t.Fatalf("switched-on reviewer launch = %+v plugins %s", launch, plugins)
	}
	launch, _ = RoleLaunch(inv, cfg, nil, "worker", "codex")
	if !reflect.DeepEqual(launch.Off, []string{"repo-a"}) {
		t.Fatalf("worker codex launch = %+v", launch)
	}
}

// Kiro gets an allowlist: a skill:// resource for each enabled skill, repo
// skills relative to the worktree, never Tallpine's own or a reviewer's repo skill.
func TestRoleLaunchKiroResourcesHonourSwitches(t *testing.T) {
	inv := roleFixture(t)
	launch, _ := RoleLaunch(inv, domain.ProjectConfig{}, nil, "worker", "kiro")
	if len(launch.Resources) != 2 || launch.Resources[0] != "skill://.claude/skills/repo-a/SKILL.md" || filepath.Base(filepath.Dir(launch.Resources[1])) != "user-b" || len(launch.Off) != 0 {
		t.Fatalf("worker kiro launch = %+v", launch)
	}
	cfg := domain.ProjectConfig{SkillRoles: domain.SkillRoles{"worker": {"repo-a": false}, "reviewer": {"repo-a": true}}}
	launch, _ = RoleLaunch(inv, cfg, nil, "worker", "kiro")
	if len(launch.Resources) != 1 || filepath.Base(filepath.Dir(launch.Resources[0])) != "user-b" {
		t.Fatalf("switched-off repo skill still listed: %+v", launch)
	}
	launch, _ = RoleLaunch(inv, cfg, nil, domain.RoleReviewer, "kiro")
	if len(launch.Resources) != 0 {
		t.Fatalf("reviewer kiro got skills it may not load: %+v", launch)
	}
}

// For every harness, source and role, the launch changes when a switch flips
// exactly where SkillEffectFor says the switch is enforced.
func TestRoleLaunchMatchesEffectTable(t *testing.T) {
	inv := roleFixture(t)
	for _, h := range domain.AllHarnesses {
		for _, role := range domain.SkillRoleNames {
			for _, s := range inv.Winners {
				src := SourceOf(s.Scope)
				on := domain.ProjectConfig{SkillRoles: domain.SkillRoles{role: {s.Name: true}}}
				off := domain.ProjectConfig{SkillRoles: domain.SkillRoles{role: {s.Name: false}}}
				a, _ := RoleLaunch(inv, on, nil, role, string(h))
				b, _ := RoleLaunch(inv, off, nil, role, string(h))
				changed := !reflect.DeepEqual(a, b)
				enforced := domain.SkillEffectFor(string(h), src, role) == domain.SkillEnforced
				if changed != enforced {
					t.Errorf("%s/%s/%s: launch changed=%v, effect %s", h, role, s.Name, changed, domain.SkillEffectFor(string(h), src, role))
				}
				if changed && h != domain.HarnessKiro && !slices.Contains(b.Off, s.Name) {
					t.Errorf("%s/%s/%s: off launch does not hide the skill: %+v", h, role, s.Name, b)
				}
			}
		}
	}
}

// A repo skill and an account skill with the same name: the reviewer never
// loads the repo copy but does load the account one, so the name is hidden
// for the reviewer by default and the page reports it enforced (review of B745).
func TestRoleLaunchHidesAccountCopyShadowedByRepoSkill(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	skill(t, filepath.Join(ws, ".claude", "skills"), "foo", "repo")
	skill(t, filepath.Join(home, ".claude", "skills"), "foo", "user")
	inv := Discover(Options{Workspace: ws, Home: home})
	for _, h := range []string{"claude-code", "codex"} {
		launch, _ := RoleLaunch(inv, domain.ProjectConfig{}, nil, domain.RoleReviewer, h)
		if !slices.Contains(launch.Off, "foo") {
			t.Errorf("%s reviewer launch does not hide the account copy: %+v", h, launch)
		}
	}
	if got := EffectFor(inv, "foo", "claude-code", domain.RoleReviewer); got != domain.SkillEnforced {
		t.Fatalf("reviewer effect for foo = %s, want enforced", got)
	}
	launch, _ := RoleLaunch(inv, domain.ProjectConfig{SkillRoles: domain.SkillRoles{domain.RoleReviewer: {"foo": true}}}, nil, domain.RoleReviewer, "kiro")
	if len(launch.Resources) != 1 || !strings.Contains(launch.Resources[0], home) {
		t.Fatalf("reviewer kiro resources = %+v, want the account copy only", launch.Resources)
	}
	launch, _ = RoleLaunch(inv, domain.ProjectConfig{}, nil, "worker", "kiro")
	if len(launch.Resources) != 1 || launch.Resources[0] != "skill://.claude/skills/foo/SKILL.md" {
		t.Fatalf("worker kiro resources = %+v, want one entry for the name", launch.Resources)
	}
}

// Kiro lists repo skills by worktree path, so an uncommitted one is left out.
func TestRoleLaunchKiroSkipsPendingRepoSkills(t *testing.T) {
	ws := t.TempDir()
	skill(t, filepath.Join(ws, ".claude", "skills"), "draft", "not committed")
	inv := Discover(Options{Workspace: ws, Home: t.TempDir(), Committed: func(string) bool { return false }})
	if launch, _ := RoleLaunch(inv, domain.ProjectConfig{}, nil, "worker", "kiro"); len(launch.Resources) != 0 {
		t.Fatalf("pending repo skill listed: %+v", launch.Resources)
	}
}

// The master switch hides a skill from every harness and role; an agent-type
// switch only from that agent (Kiro drops its resource).
func TestRoleLaunchMasterAndAgentSwitches(t *testing.T) {
	inv := roleFixture(t)
	cfg := domain.ProjectConfig{SkillRoles: domain.SkillRoles{
		domain.SkillMasterKey:         {"repo-a": false},
		domain.SkillAgentKey("codex"): {"user-b": false},
		domain.SkillAgentKey("kiro"):  {"user-b": false},
		"reviewer":                    {"repo-a": true},
	}}
	for _, harness := range []string{"claude-code", "codex"} {
		launch, _ := RoleLaunch(inv, cfg, nil, "worker", harness)
		want := []string{"repo-a"}
		if harness == "codex" {
			want = []string{"repo-a", "user-b", "user-b-decl"}
		}
		if !reflect.DeepEqual(launch.Off, want) {
			t.Errorf("%s worker off = %v, want %v", harness, launch.Off, want)
		}
	}
	launch, _ := RoleLaunch(inv, cfg, nil, "worker", "kiro")
	if len(launch.Resources) != 0 {
		t.Errorf("kiro worker resources = %v, want none", launch.Resources)
	}
}
