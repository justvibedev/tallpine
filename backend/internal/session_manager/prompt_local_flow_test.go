package sessionmanager

import (
	"strings"
	"testing"
)

func TestPrompt_LocalBaseWordingAndNoPushToLocalRemote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, repo string
		tracked    bool
	}{
		{"empty", "", false},
		{"path", "/Users/me/repos/proj.git", false},
		{"file url", "file:///Users/me/repos/proj.git", false},
		{"github", "https://github.com/acme/proj.git", true},
	}
	t.Run("auto default branch is not guessed", func(t *testing.T) {
		t.Parallel()
		got := buildSystemPromptText(systemPromptConfig{Role: sessionPromptRoleWorker, Project: promptProject{ID: "p", Name: "P"}})
		if !strings.Contains(got, "updated to the local default branch") || strings.Contains(got, "local main") {
			t.Fatal("empty default branch must not name main")
		}
	})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			project := promptProject{ID: "p", Name: "P", Repo: tc.repo, DefaultBranch: "main"}
			orch := buildSystemPromptText(systemPromptConfig{Role: sessionPromptRoleOrchestrator, Project: project})
			worker := buildSystemPromptText(systemPromptConfig{Role: sessionPromptRoleWorker, Project: project})
			if tc.tracked {
				for _, got := range []string{orch, worker} {
					if !strings.Contains(got, "updated to origin/main") {
						t.Fatal("tracked project lost origin/main wording")
					}
				}
				if !strings.Contains(orch, "update your worktree to origin's default branch") {
					t.Fatal("tracked orchestrator bullet changed")
				}
				if !strings.Contains(orch, "git fetch origin && git merge --ff-only origin/main") || strings.Contains(worker, "## Local Git Rules") {
					t.Fatal("tracked project text changed")
				}
				return
			}
			for _, got := range []string{orch, worker} {
				if !strings.Contains(got, "updated to local main") || strings.Contains(got, "origin/main") {
					t.Fatalf("remoteless prompt mentions origin: %q", got)
				}
			}
			if !strings.Contains(orch, "update your worktree to local main") || strings.Contains(orch, "origin's default branch") {
				t.Fatal("orchestrator worktree bullet still names origin")
			}
			if strings.Contains(worker, "git push") || strings.Contains(worker, "## Tallpine Native Review") {
				t.Fatal("remoteless worker told to push or use PR review")
			}
			if strings.Contains(orch, "git fetch origin") {
				t.Fatal("orchestrator workflow still fetches origin")
			}
			if !strings.Contains(worker, "## Local Git Rules") {
				t.Fatal("worker missing Local Git Rules")
			}
		})
	}
}

func TestPrompt_WorkerGetsLocalReviewText(t *testing.T) {
	t.Parallel()
	for _, repo := range []string{"", "https://github.com/acme/proj.git"} {
		got := buildSystemPromptText(systemPromptConfig{Role: sessionPromptRoleWorker, Project: promptProject{ID: "p", Name: "P", Repo: repo, DefaultBranch: "main"}})
		for _, want := range []string{"## Tasks without a PR", "commit your work on your own branch", "report ready", "path:line - finding"} {
			if !strings.Contains(got, want) {
				t.Fatalf("repo %q: worker prompt lacks %q", repo, want)
			}
		}
	}
}
