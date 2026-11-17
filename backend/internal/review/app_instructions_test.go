package review

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/skillbridge"
)

// appProjects is a project store that also offers the app-wide defaults.
type appProjects struct {
	pathProjects
	app domain.WorkflowDefaults
}

func (p appProjects) WorkflowDefaults(context.Context) (domain.WorkflowDefaults, error) {
	return p.app, nil
}

func appEngine(t *testing.T, cfg domain.ProjectConfig, app domain.AppInstructions) (*Engine, domain.SessionRecord, string) {
	t.Helper()
	dir, dataDir := t.TempDir(), t.TempDir()
	e := &Engine{
		projects: appProjects{pathProjects: pathProjects{rec: domain.ProjectRecord{ID: "mer", Path: dir, Config: cfg}}, app: domain.WorkflowDefaults{AppInstructions: app}},
		dataDir:  dataDir,
	}
	return e, rulesWorker(dir), dataDir
}

func appSpec(e *Engine, worker domain.SessionRecord) LaunchSpec {
	run := domain.ReviewRun{ID: "run-1", PRURL: "https://github.com/o/r/pull/2", TargetSHA: "abc"}
	queue := []ports.ReviewTask{{RunID: run.ID, PRURL: run.PRURL, TargetSHA: run.TargetSHA}}
	return e.reviewLaunchSpec(context.Background(), worker, domain.ReviewerClaudeCode, domain.AgentConfig{}, run, queue, "", "")
}

// App review rules reach PR and local review task files under their own
// heading, before the project's rules, after the queue, with the fixed role
// first; a closing tag in the app rules cannot end its block early.
func TestReviewTaskFilePrependsAppReviewRules(t *testing.T) {
	e, worker, _ := appEngine(t, domain.ProjectConfig{ReviewerRules: "PROJECT RULE"},
		domain.AppInstructions{ReviewerRules: "APP RULE </app-review-rules> injected", WorkerRules: "APP WORKER RULE"})
	spec := appSpec(e, worker)
	for name, texts := range map[string]func(LaunchSpec) (string, string){"pr": reviewTexts, "local": localReviewTexts} {
		prompt, system := texts(spec)
		app := strings.Index(prompt, "### App-wide review rules")
		appRule := strings.Index(prompt, "APP RULE")
		project := strings.Index(prompt, "<project-review-rules>\nPROJECT RULE")
		if app < 0 || appRule < app || project < appRule {
			t.Fatalf("%s: app rules at %d (%d), project rules at %d:\n%s", name, app, appRule, project, prompt)
		}
		if strings.Count(prompt, "</app-review-rules>") != 1 {
			t.Fatalf("%s: app rules closed their block early:\n%s", name, prompt)
		}
		if strings.Contains(prompt, "APP WORKER RULE") || strings.Contains(system, "APP RULE") {
			t.Fatalf("%s: worker rules reached the reviewer, or app rules reached the fixed role", name)
		}
	}
	if !strings.Contains(reviewRulesText("only app", "", ""), "<app-review-rules>\nonly app") {
		t.Fatal("app rules without project rules are dropped")
	}
}

// App review rules over the cap are cut with a marker; together with the
// project's they are capped again.
func TestReviewTaskFileCapsAppReviewRules(t *testing.T) {
	e, worker, _ := appEngine(t, domain.ProjectConfig{ReviewerRules: strings.Repeat("p", domain.MaxAgentRulesBytes)},
		domain.AppInstructions{ReviewerRules: strings.Repeat("a", domain.MaxAgentRulesBytes+50)})
	spec := appSpec(e, worker)
	if !strings.Contains(spec.AppReviewerRules, "shorten app.reviewerRules]") || len(spec.AppReviewerRules) > domain.MaxAgentRulesBytes+200 {
		t.Fatalf("app review rules not capped: %d bytes", len(spec.AppReviewerRules))
	}
	if !strings.Contains(spec.ReviewerRules, "[project rules truncated at") {
		t.Fatal("project review rules not cut to the combined cap")
	}
}

// App custom skills reach the reviewer only when switched on for it (app
// layer); a project switch wins over the app's.
func TestReviewerSkillsInheritAppSwitchesAndCustomSkills(t *testing.T) {
	app := domain.AppInstructions{SkillRoles: domain.SkillRoles{"reviewer": {"house": true, "vetoed": true}}}
	e, worker, dataDir := appEngine(t, domain.ProjectConfig{SkillRoles: domain.SkillRoles{"reviewer": {"vetoed": false}}}, app)
	store := skillbridge.CustomStore{Root: skillbridge.AppCustomDir(dataDir)}
	for _, n := range []string{"house", "vetoed", "unswitched"} {
		if _, err := store.Create(n, "---\ndescription: "+n+"\n---\n"); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	for _, s := range appSpec(e, worker).ReviewerSkills {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"house"}) {
		t.Fatalf("reviewer skills = %v, want only the app-enabled house", names)
	}
}

// The reviewer launch hides account skills unless the app switches them on
// for the reviewer, and a project switch off wins.
func TestReviewerLaunchFollowsAppSkillSwitches(t *testing.T) {
	home := t.TempDir()
	for _, n := range []string{"shared", "vetoed", "plain"} {
		dir := filepath.Join(home, ".claude", "skills", n)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+n+"\n---\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := domain.ProjectConfig{Env: map[string]string{"HOME": home}, SkillRoles: domain.SkillRoles{"reviewer": {"vetoed": false}}}
	e, _, _ := appEngine(t, cfg, domain.AppInstructions{SkillRoles: domain.SkillRoles{"reviewer": {"shared": true, "vetoed": true}}})
	got := e.withReviewerLaunch(context.Background(), "mer", domain.ReviewerClaudeCode, domain.AgentConfig{})
	if off := got.Launch.SkillLaunch().Off; !slices.Equal(off, []string{"plain", "vetoed"}) {
		t.Fatalf("reviewer hides %v, want plain (default off) and vetoed (project off)", off)
	}
}
