package domain

import (
	"reflect"
	"testing"
)

// A role's switch is the project value, else the app value, else the role
// default: on for workers and orchestrators, off for reviewers (B740 q1).
func TestSkillOnResolutionOrderPerRole(t *testing.T) {
	project := SkillRoles{"worker": {"a": false}, "reviewer": {"b": true}}
	app := SkillRoles{"worker": {"a": true, "c": false}, "orchestrator": {"a": false}, "reviewer": {"c": true}}
	cases := []struct {
		role, id string
		want     bool
	}{
		{"worker", "a", false},       // project wins over app
		{"worker", "c", false},       // app when the project has none
		{"worker", "z", true},        // default on
		{"orchestrator", "a", false}, // app layer, other role untouched by worker's
		{"orchestrator", "z", true},
		{"reviewer", "b", true},  // project switched on
		{"reviewer", "c", true},  // app switched on
		{"reviewer", "a", false}, // default off, worker's value does not leak
	}
	for _, c := range cases {
		if got := SkillOn(project, app, c.role, "", c.id, SkillSourceUser); got != c.want {
			t.Errorf("SkillOn(%s, %s) = %v, want %v", c.role, c.id, got, c.want)
		}
	}
	if _, set := ResolveSkillRole(project, nil, "reviewer", "z"); set {
		t.Fatal("an unset skill reported as set")
	}
}

// The effect table mirrors the launch code (see skillbridge.RoleLaunch and the
// adapters); TestRoleLaunchMatchesEffectTable checks the launch side.
func TestSkillEffectForTable(t *testing.T) {
	cases := []struct {
		harness string
		src     SkillSource
		role    string
		want    SkillEffect
	}{
		{"claude-code", SkillSourceUser, "worker", SkillEnforced},
		{"claude-code", SkillSourceRepo, "orchestrator", SkillEnforced},
		{"claude-code", SkillSourceRepo, "reviewer", SkillUnsupported},
		{"claude-code", SkillSourceUser, "reviewer", SkillEnforced},
		{"claude-code", SkillSourcePlugin, "reviewer", SkillEnforced},
		{"codex", SkillSourcePlugin, "worker", SkillUnsupported},
		{"codex", SkillSourceUser, "reviewer", SkillEnforced},
		{"kiro", SkillSourceRepo, "worker", SkillEnforced},
		{"kiro", SkillSourceUser, "reviewer", SkillEnforced},
		{"deepseek-harness", SkillSourceRepo, "worker", SkillAdvisory},
		{"deepseek-harness", SkillSourceUser, "orchestrator", SkillAdvisory},
		{"deepseek-harness", SkillSourceUser, "reviewer", SkillUnsupported},
		{"claude-code", SkillSourceTallpine, "worker", SkillUnsupported},
		{"deepseek-harness", SkillSourceCustom, "reviewer", SkillEnforced},
		{"codex", SkillSourceCustom, "worker", SkillEnforced},
		{"kiro", SkillSourceReviewerRepo, "reviewer", SkillEnforced},
		{"claude-code", SkillSourceReviewerRepo, "worker", SkillUnsupported},
	}
	for _, c := range cases {
		if got := SkillEffectFor(c.harness, c.src, c.role); got != c.want {
			t.Errorf("SkillEffectFor(%s, %s, %s) = %s, want %s", c.harness, c.src, c.role, got, c.want)
		}
	}
}

func TestSkillsOffForAndLaunchEncoding(t *testing.T) {
	off := SkillsOffFor(SkillRoles{"worker": {"b": false, "a": true}}, SkillRoles{"worker": {"c": false, "b": true}}, "worker", "")
	if !reflect.DeepEqual(off, []string{"b", "c"}) {
		t.Fatalf("SkillsOffFor = %v", off)
	}
	if (SkillLaunch{}).Encode() != "" {
		t.Fatal("empty launch encodes to text")
	}
	in := SkillLaunch{Off: []string{"x"}, Resources: []string{"skill:///a/SKILL.md"}}
	if got := (AgentLaunchOptions{Skills: in.Encode()}).SkillLaunch(); !reflect.DeepEqual(got, in) {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestSkillDefaultNewSources(t *testing.T) {
	if !SkillDefault(RoleReviewer, SkillSourceReviewerRepo) || SkillDefault(SkillRoleWorker, SkillSourceReviewerRepo) {
		t.Fatal("reviewer-repo must default on for the reviewer only (n3)")
	}
	if SkillDefault(RoleReviewer, SkillSourceCustom) || !SkillDefault(SkillRoleWorker, SkillSourceCustom) {
		t.Fatal("custom skills default off for the reviewer (q1), on for workers")
	}
}

// The master switch cuts a skill off for every role and agent; an agent-type
// switch only for that agent. Project beats app, unset is on.
func TestSkillMasterAndAgentSwitches(t *testing.T) {
	app := SkillRoles{SkillMasterKey: {"m": false, "p": false}, SkillAgentKey("codex"): {"a": false}}
	project := SkillRoles{SkillMasterKey: {"p": true}, SkillAgentKey("kiro"): {"a": false}}
	cases := []struct {
		role, harness, id string
		want              bool
	}{
		{"worker", "claude-code", "m", false},
		{"reviewer", "claude-code", "m", false},
		{"orchestrator", "codex", "m", false},
		{"worker", "codex", "a", false},
		{"worker", "kiro", "a", false},
		{"worker", "claude-code", "a", true},
		{"worker", "deepseek-harness", "a", true},
		{"worker", "codex", "p", true}, // project master on beats the app's off
		{"worker", "", "a", true},      // no harness: only the master applies
	}
	for _, c := range cases {
		if got := SkillOn(project, app, c.role, c.harness, c.id, SkillSourceUser); got != c.want {
			t.Errorf("SkillOn(%s, %s, %s) = %v, want %v", c.role, c.harness, c.id, got, c.want)
		}
	}
	// A reviewer switch on does not outrank the master switch.
	if SkillOn(SkillRoles{"reviewer": {"m": true}}, app, RoleReviewer, "codex", "m", SkillSourceCustom) {
		t.Error("a reviewer's on switch beat the master off")
	}
	if got := SkillsOffFor(project, app, "worker", "codex"); !reflect.DeepEqual(got, []string{"a", "m"}) {
		t.Errorf("SkillsOffFor codex = %v", got)
	}
	if got := SkillsOffFor(project, app, "worker", "claude-code"); !reflect.DeepEqual(got, []string{"m"}) {
		t.Errorf("SkillsOffFor claude = %v", got)
	}
}

func TestSkillRolesValidateAcceptsMasterAndAgents(t *testing.T) {
	ok := SkillRoles{SkillMasterKey: {"x": false}, SkillAgentKey("codex"): {"x": false}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"agent:codex", "agentNope", "everyone"} {
		if err := (SkillRoles{bad: {"x": false}}).Validate(); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
