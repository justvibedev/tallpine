package sessionmanager

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

// A worker or orchestrator prompt names the skills switched off for its role:
// the only switch DeepSeek honours (advisory), repeated where it is enforced.
func TestSystemPromptNamesSkillsSwitchedOffForTheRole(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{SkillRoles: domain.SkillRoles{
		"worker": {"design": false, "kept": true}, "orchestrator": {"planner\nx": false},
	}}}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})
	const marker = "## Skills switched off for this session"
	worker, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-1", "", false)
	if err != nil || !strings.Contains(worker, marker) || !strings.Contains(worker, `- "design"`) || strings.Contains(worker, `"kept"`) {
		t.Fatalf("worker prompt (err %v) lacks the off skills:\n%s", err, worker)
	}
	orch, _ := m.buildSystemPrompt(ctx, domain.KindOrchestrator, "mer", "mer-2", "", false)
	if !strings.Contains(orch, `- "planner\nx"`) || strings.Contains(orch, `"design"`) {
		t.Fatalf("orchestrator prompt has the wrong off skills:\n%s", orch)
	}
	delete(st.projects, "mer")
	if plain, _ := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-3", "", false); strings.Contains(plain, marker) {
		t.Fatal("prompt names off skills with no switch set")
	}
}

// A fresh launch carries the role's skill switches for its harness, found by
// the same discovery the skills page uses.
func TestFreshLaunchOptionsCarriesRoleSkillSwitches(t *testing.T) {
	m, st, _, _ := newManager()
	ctx := context.Background()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".claude", "skills", "design"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".claude", "skills", "design", "SKILL.md"), []byte("---\nname: design\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := domain.ProjectConfig{Env: map[string]string{"HOME": t.TempDir()}, SkillRoles: domain.SkillRoles{"worker": {"design": false}}}
	st.projects["p"] = domain.ProjectRecord{ID: "p", Path: ws, Config: project}
	got := m.freshLaunchOptions(ctx, domain.HarnessClaudeCode, domain.KindWorker, "p", project, "", domain.AgentLaunchOptions{})
	if !slices.Equal(got.SkillLaunch().Off, []string{"design"}) {
		t.Fatalf("worker launch skills = %+v", got.SkillLaunch())
	}
	got = m.freshLaunchOptions(ctx, domain.HarnessKiro, domain.KindOrchestrator, "p", project, "", domain.AgentLaunchOptions{})
	if !slices.Equal(got.SkillLaunch().Resources, []string{"skill://.claude/skills/design/SKILL.md"}) {
		t.Fatalf("orchestrator kiro launch = %+v", got.SkillLaunch())
	}
}

// A Kiro launch lists the repo skills its worktree will have: those on the
// ref sessions start from, not the checkout's HEAD. Here the checkout is on
// main (which has neither skill) while sessions start from base, which has
// "shared"; "local-only" exists only untracked in the checkout.
func TestKiroLaunchJudgesPendingSkillsAgainstSessionBase(t *testing.T) {
	m, st, _, fw := newManager()
	ctx := context.Background()
	repo := newManagerGitRepo(t)
	writeSkill := func(name string) {
		dir := filepath.Join(repo, ".claude", "skills", name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runManagerGit(t, repo, "checkout", "-q", "-b", "base")
	writeSkill("shared")
	runManagerGit(t, repo, "add", ".claude")
	runManagerGit(t, repo, "commit", "-q", "-m", "shared skill")
	runManagerGit(t, repo, "checkout", "-q", "main")
	writeSkill("shared")
	writeSkill("local-only")
	fw.resolved = map[string]ports.WorkspaceDefaultBranch{repo: {Branch: "base", BaseRef: "refs/heads/base"}}
	project := domain.ProjectConfig{DefaultBranch: "base", Env: map[string]string{"HOME": t.TempDir()}}
	st.projects["p"] = domain.ProjectRecord{ID: "p", Path: repo, Config: project}
	got := m.freshLaunchOptions(ctx, domain.HarnessKiro, domain.KindWorker, "p", project, "", domain.AgentLaunchOptions{})
	if want := []string{"skill://.claude/skills/shared/SKILL.md"}; !slices.Equal(got.SkillLaunch().Resources, want) {
		t.Fatalf("kiro launch resources = %v, want %v", got.SkillLaunch().Resources, want)
	}
	if len(fw.resolves) == 0 || fw.resolves[len(fw.resolves)-1].configuredBranch != "base" {
		t.Fatalf("resolves = %+v, want the project's base branch", fw.resolves)
	}
}

// Chat sessions get the same switches through the role settings env.
func TestPinClaudeRoleSettingsHidesOffSkills(t *testing.T) {
	m, _, _, _ := newManager()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".claude", "skills", "design"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".claude", "skills", "design", "SKILL.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := domain.ProjectConfig{Env: map[string]string{"HOME": t.TempDir()}, SkillRoles: domain.SkillRoles{"worker": {"design": false}}}
	env := map[string]string{}
	pinClaudeRoleSettings(env, domain.HarnessClaudeCode, domain.KindWorker, cfg, nil, m.skillInventory(ws, cfg.Env))
	if got := env[domain.EnvClaudeRoleSettings]; !strings.Contains(got, `"skillOverrides":{"design":"off"}`) {
		t.Fatalf("chat role settings = %q", got)
	}
}

// Workers and orchestrators get the project's custom skills switched on for
// their role in the "Skills available to you" section, by SKILL.md path.
func TestSystemPromptListsEnabledCustomSkills(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store := skillbridge.CustomStore{Root: skillbridge.CustomDir(dataDir, "mer")}
	for _, n := range []string{"sql-checks", "orch-only"} {
		if _, err := store.Create(n, "---\ndescription: about "+n+"\n---\n"); err != nil {
			t.Fatal(err)
		}
	}
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{SkillRoles: domain.SkillRoles{"worker": {"orch-only": false}}}}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath, DataDir: dataDir})
	const marker = "## Skills available to you"
	worker, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-1", "", false)
	sql := `- sql-checks: "` + filepath.Join(store.Root, "sql-checks", "SKILL.md") + `"`
	if err != nil || !strings.Contains(worker, marker) || !strings.Contains(worker, sql) || !strings.Contains(worker, "about sql-checks") || strings.Contains(worker, "- orch-only:") {
		t.Fatalf("worker prompt (err %v):\n%s", err, worker)
	}
	orch, _ := m.buildSystemPrompt(ctx, domain.KindOrchestrator, "mer", "mer-2", "", false)
	if !strings.Contains(orch, "- orch-only:") || !strings.Contains(orch, sql) {
		t.Fatalf("orchestrator prompt:\n%s", orch)
	}
}

// The master and agent-type switches reach the advisory prompt section: a
// skill off for the agent (or everywhere) is named whatever its role switch says.
func TestSkillsOffSectionNamesMasterAndAgentSwitches(t *testing.T) {
	project := domain.ProjectConfig{SkillRoles: domain.SkillRoles{
		domain.SkillMasterKey:                            {"everywhere": false},
		domain.SkillAgentKey(string(domain.HarnessKiro)): {"not-kiro": false},
	}}
	deepseek := skillsOffSection(project, nil, domain.KindWorker, domain.HarnessDeepSeek)
	if !strings.Contains(deepseek, `"everywhere"`) || strings.Contains(deepseek, "not-kiro") {
		t.Fatalf("deepseek section = %q", deepseek)
	}
	kiro := skillsOffSection(project, nil, domain.KindOrchestrator, domain.HarnessKiro)
	if !strings.Contains(kiro, `"everywhere"`) || !strings.Contains(kiro, `"not-kiro"`) {
		t.Fatalf("kiro section = %q", kiro)
	}
}

// A custom skill is delivered only through the prompt, so its agent-type
// switch must act there: off for Codex it is left out of a Codex prompt (the
// harness passed in, not read from the session row, which a prepared task
// seeds without one) and still listed for Claude Code.
func TestSystemPromptHonoursAgentSwitchForCustomSkill(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store := skillbridge.CustomStore{Root: skillbridge.CustomDir(dataDir, "mer")}
	dir := filepath.Join(store.Root, "sql-checks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: sql-checks\ndescription: about sql-checks\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{SkillRoles: domain.SkillRoles{domain.SkillAgentKey("codex"): {"sql-checks": false}}}}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath, DataDir: dataDir})
	codex, err := m.buildSystemPromptFor(ctx, domain.KindWorker, "mer", "mer-1", "", "", domain.HarnessCodex, false)
	if err != nil || strings.Contains(codex, "- sql-checks:") {
		t.Fatalf("codex prompt lists the skill (err %v):\n%s", err, codex)
	}
	claude, _ := m.buildSystemPromptFor(ctx, domain.KindWorker, "mer", "mer-2", "", "", domain.HarnessClaudeCode, false)
	if !strings.Contains(claude, "- sql-checks:") {
		t.Fatalf("claude prompt lacks the skill:\n%s", claude)
	}
}
