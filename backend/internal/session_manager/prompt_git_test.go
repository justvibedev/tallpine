package sessionmanager

import (
	"strings"
	"testing"
)

func TestBuildSystemPrompt_OrchestratorForbidsRemoteChangesAndUnaskedPublishing(t *testing.T) {
	t.Parallel()

	got := buildSystemPromptText(systemPromptConfig{
		Role:    sessionPromptRoleOrchestrator,
		Project: promptProject{ID: "mer", Name: "Mercury"},
	})
	for _, want := range []string{
		"## Owner Checkout and Git Remotes",
		"Never run 'git remote add', 'git remote set-url', 'git remote rename' or 'git remote remove' unless the owner explicitly asked",
		"unless the owner asked for it in this conversation",
		"never write repository config ('git config', 'git config --local') from your session or any worktree; use 'git config --worktree' for session-only settings",
		"Never use 'git push --all' or '--mirror'",
		"These bans also apply to work you delegate",
		"only for a repository the project already uses or one the owner named",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("orchestrator prompt missing %q", want)
		}
	}
}

func TestBuildSystemPrompt_WorkerForbidsNewHostPublishing(t *testing.T) {
	t.Parallel()

	got := buildSystemPromptText(systemPromptConfig{
		Role:                  sessionPromptRoleWorker,
		Project:               promptProject{ID: "mer", Name: "Mercury", Repo: "https://github.com/acme/mercury"},
		OrchestratorSessionID: "mer-orchestrator",
	})
	for _, want := range []string{
		"push source or branches to a repository the project does not already use (a new host, or an unrelated repo on a known host) unless the owner asked",
		"never push with '--all' or '--mirror'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("worker prompt missing %q", want)
		}
	}
}

func TestBuildSystemPrompt_OrchestratorGitHubAccountRule(t *testing.T) {
	t.Parallel()

	got := buildSystemPromptText(systemPromptConfig{
		Role:    sessionPromptRoleOrchestrator,
		Project: promptProject{ID: "mer", Name: "Mercury"},
	})
	for _, want := range []string{
		"## GitHub Account",
		"only when the owner asked for it in this conversation",
		"tallpine github accounts",
		"--github-user <login>",
		"Never use 'gh auth switch', 'gh auth login' or 'gh auth logout'",
		"reaches only sessions started afterwards",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("orchestrator prompt missing %q", want)
		}
	}
}
