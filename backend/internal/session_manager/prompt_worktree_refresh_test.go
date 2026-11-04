package sessionmanager

import (
	"strings"
	"testing"
)

// B190: orchestrator worktrees are long-lived, so the orchestrator prompt must
// tell the agent to update its worktree before running repo scripts or giving
// the owner instructions. Workers get a fresh worktree and don't need it.
func TestBuildSystemPrompt_OrchestratorUpdatesWorktreeBeforeScripts(t *testing.T) {
	t.Parallel()

	const want = "Before running repo scripts or reading code to give the owner instructions, update your worktree to origin's default branch."
	project := promptProject{ID: "mer", Name: "Mercury", Repo: "https://github.com/acme/mercury", DefaultBranch: "main", Path: "/repo/mercury"}
	orch := buildSystemPromptText(systemPromptConfig{Role: sessionPromptRoleOrchestrator, Project: project})
	if !strings.Contains(orch, want) {
		t.Fatalf("orchestrator prompt missing %q:\n%s", want, orch)
	}
	worker := buildSystemPromptText(systemPromptConfig{Role: sessionPromptRoleWorker, Project: project})
	if strings.Contains(worker, want) {
		t.Fatalf("worker prompt unexpectedly contains orchestrator worktree rule")
	}
}
