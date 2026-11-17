package review

import (
	stdctx "context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// A native review launch takes the project's reviewer-role plugin and
// auto-memory overrides; an unset role leaves the launch unchanged.
func TestReviewLaunchSpecAppliesReviewerClaudeLaunch(t *testing.T) {
	ctx := stdctx.Background()
	worker := domain.SessionRecord{ID: "w1", ProjectID: "p1"}
	cfg := domain.ProjectConfig{
		ClaudePlugins:    map[string]map[string]bool{"reviewer": {"impeccable@x": false}},
		ClaudeAutoMemory: map[string]bool{"reviewer": false},
	}
	e := &Engine{projects: fakeProjects{cfg: cfg}}
	spec := e.reviewLaunchSpec(ctx, worker, domain.ReviewerClaudeCode, domain.AgentConfig{Model: "m"}, domain.ReviewRun{}, nil, "", "")
	if spec.AgentConfig.Launch.EnabledPlugins != `{"impeccable@x":false}` || !spec.AgentConfig.Launch.AutoMemoryOff || spec.AgentConfig.Model != "m" {
		t.Fatalf("reviewer launch = %+v", spec.AgentConfig)
	}
	e = &Engine{projects: fakeProjects{}}
	spec = e.reviewLaunchSpec(ctx, worker, domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewRun{}, nil, "", "")
	if spec.AgentConfig.Launch.EnabledPlugins != "" || spec.AgentConfig.Launch.AutoMemoryOff {
		t.Fatalf("unset reviewer role changed the launch: %+v", spec.AgentConfig.Launch)
	}
}

// The reviewer gets only what is switched on for it (B740 q1): a Claude
// reviewer launch turns every account plugin off and hides every account
// skill, unless the project switches it on for the reviewer role.
func TestReviewLaunchSpecDefaultsAccountSkillsAndPluginsOff(t *testing.T) {
	ctx := stdctx.Background()
	home := t.TempDir()
	claude := filepath.Join(home, ".claude")
	mustWrite(t, filepath.Join(claude, "settings.json"), `{"enabledPlugins":{"p@m":true,"q@m":true}}`)
	mustWrite(t, filepath.Join(claude, "skills", "design", "SKILL.md"), "---\nname: design\n---\n")
	env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": claude}
	worker := domain.SessionRecord{ID: "w1", ProjectID: "p1"}

	e := &Engine{projects: fakeProjects{cfg: domain.ProjectConfig{Env: env}}}
	launch := e.reviewLaunchSpec(ctx, worker, domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewRun{}, nil, "", "").AgentConfig.Launch
	if launch.EnabledPlugins != `{"p@m":false,"q@m":false}` || !slices.Equal(launch.SkillLaunch().Off, []string{"design"}) {
		t.Fatalf("default reviewer launch = plugins %s skills %+v", launch.EnabledPlugins, launch.SkillLaunch())
	}

	cfg := domain.ProjectConfig{Env: env, ClaudePlugins: map[string]map[string]bool{"reviewer": {"p@m": true}}, SkillRoles: domain.SkillRoles{"reviewer": {"design": true}}}
	e = &Engine{projects: fakeProjects{cfg: cfg}}
	launch = e.reviewLaunchSpec(ctx, worker, domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewRun{}, nil, "", "").AgentConfig.Launch
	if launch.EnabledPlugins != `{"p@m":true,"q@m":false}` || len(launch.SkillLaunch().Off) != 0 {
		t.Fatalf("switched-on reviewer launch = plugins %s skills %+v", launch.EnabledPlugins, launch.SkillLaunch())
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
