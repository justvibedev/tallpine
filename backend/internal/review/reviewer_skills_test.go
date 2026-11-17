package review

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/workspace/gitworktree"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/pathx"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/skillbridge"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

func commitSkill(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, skillbridge.ReviewerSkillsDir, name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rulesGit(t, dir, "add", "-A")
	rulesGit(t, dir, "commit", "-q", "-m", name+" "+body)
}

// skillsRepo is rulesRepo for reviewer skills: main, the PR head and the
// worktree each carry a different "base" skill, and main is advanced from
// another clone so only a fetch sees the newest version.
func skillsRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	project := filepath.Join(root, "project")
	other := filepath.Join(root, "other")
	rulesGit(t, root, "init", "-q", "--bare", "-b", "main", origin)
	rulesGit(t, root, "clone", "-q", origin, project)
	rulesGit(t, project, "checkout", "-q", "-b", "main")
	commitSkill(t, project, "base", "---\ndescription: base checks\n---\nBASE v1")
	commitSkill(t, project, "off", "OFF SKILL")
	rulesGit(t, project, "push", "-q", "origin", "main")
	rulesGit(t, project, "checkout", "-q", "-b", "pr-head")
	commitSkill(t, project, "base", "HEAD SKILL")
	commitSkill(t, project, "headonly", "HEAD ONLY SKILL")
	rulesGit(t, project, "push", "-q", "origin", "pr-head")
	if err := os.WriteFile(filepath.Join(project, skillbridge.ReviewerSkillsDir, "base", "SKILL.md"), []byte("WORKTREE SKILL"), 0o644); err != nil {
		t.Fatal(err)
	}
	rulesGit(t, root, "clone", "-q", "-b", "main", origin, other)
	commitSkill(t, other, "base", "---\ndescription: base checks\n---\nBASE v2")
	rulesGit(t, other, "push", "-q", "origin", "main")
	return project
}

var skillLine = regexp.MustCompile(`(?m)^- ([a-z0-9._-]+): "([^"]+)"$`)

// Every reviewer harness gets the same task-file section: the enabled skills,
// copied text-only from the default branch and the custom store, by paths
// under the reviewer prompt root that the Security mode fence leaves readable.
func TestReviewerSkillsReachEveryHarnessFromTheDefaultBranch(t *testing.T) {
	project := skillsRepo(t)
	dataDir := t.TempDir()
	store := skillbridge.CustomStore{Root: skillbridge.CustomDir(dataDir, "mer")}
	if _, err := store.Create("mine", "---\ndescription: owner checks\n---\nMINE"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("notmine", "NOT MINE"); err != nil {
		t.Fatal(err)
	}
	cfg := domain.ProjectConfig{DefaultBranch: "main", SkillRoles: domain.SkillRoles{domain.RoleReviewer: {"mine": true, "off": false}}}
	e := &Engine{
		projects:        pathProjects{rec: domain.ProjectRecord{ID: "mer", Path: project, Config: cfg}},
		defaultBranches: gitworktree.NewDefaultBranchResolver(""),
		dataDir:         dataDir,
	}
	worker := rulesWorker(project)
	for _, prURL := range []string{"https://github.com/o/r/pull/2", "local:tp/w1"} {
		run := domain.ReviewRun{ID: "run-1", BatchID: "batch-1", PRURL: prURL, TargetSHA: "abc"}
		queue := []ports.ReviewTask{{RunID: run.ID, PRURL: run.PRURL, TargetSHA: run.TargetSHA}}
		for _, h := range []domain.ReviewerHarness{domain.ReviewerClaudeCode, domain.ReviewerCodex, domain.ReviewerKiro, domain.ReviewerDeepSeek} {
			spec := e.reviewLaunchSpec(context.Background(), worker, h, domain.AgentConfig{}, run, queue, "", "")
			l := NewLauncher(fakeReviewerResolver{reviewer: &fakeReviewer{}, ok: true}, &fakeRuntime{}, dataDir).(*agentLauncher)
			inv, err := l.prepareInvocation(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			task, err := os.ReadFile(inv.TaskPromptFile)
			if err != nil {
				t.Fatal(err)
			}
			assertReviewerSkills(t, string(task), inv.TaskPromptRoot, dataDir)
		}
	}
}

func assertReviewerSkills(t *testing.T, task, promptRoot, dataDir string) {
	t.Helper()
	if !strings.Contains(task, "## Skills available to you") {
		t.Fatalf("task lacks the skills section:\n%s", task)
	}
	for _, bad := range []string{"notmine", "- off:", "OFF SKILL", "headonly", "HEAD SKILL", "WORKTREE"} {
		if strings.Contains(task, bad) {
			t.Fatalf("task names %q:\n%s", bad, task)
		}
	}
	denied := statefence.StatePaths(filepath.Join(filepath.Dir(dataDir), "running.json"), dataDir)
	want := map[string]string{"base": "BASE v2", "mine": "MINE"}
	for _, m := range skillLine.FindAllStringSubmatch(task, -1) {
		name, path := m[1], m[2]
		body, err := os.ReadFile(path)
		if err != nil || !strings.HasSuffix(strings.TrimSpace(string(body)), want[name]) {
			t.Fatalf("%s copy %s = %q %v", name, path, body, err)
		}
		delete(want, name)
		if !pathx.Within(promptRoot, path) {
			t.Fatalf("%s copy %s is outside the prompt root %s", name, path, promptRoot)
		}
		for _, d := range append(denied.Dirs, denied.Files...) {
			if pathx.Within(d, path) {
				t.Fatalf("%s copy %s is under fenced state %s", name, path, d)
			}
		}
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Fatalf("%s copy mode %o", name, st.Mode().Perm())
		}
	}
	if len(want) != 0 {
		t.Fatalf("skills missing from the task: %v\n%s", want, task)
	}
}

// A PR touching the reviewer skills gets the stronger reviewer.
func TestReviewerSkillsDirIsARiskPath(t *testing.T) {
	file, pattern, ok := matchReviewerRisk(domain.ProjectConfig{}, nil, []string{"a.go", skillbridge.ReviewerSkillsDir + "/x/SKILL.md"})
	if !ok || pattern != skillbridge.ReviewerSkillsDir+"/**" || !strings.HasSuffix(file, "SKILL.md") {
		t.Fatalf("risk match = %q %q %v", file, pattern, ok)
	}
	if _, _, ok := matchReviewerRisk(domain.ProjectConfig{}, nil, []string{".tallpine/reviewer-skillsX/a.md"}); ok {
		t.Fatal("prefix sibling matched")
	}
}
