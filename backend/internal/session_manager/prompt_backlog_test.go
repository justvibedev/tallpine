package sessionmanager

import (
	"strings"
	"testing"
)

// backlogPromptLine is the constant orchestrator line from the backlog design
// (section 4.5), verbatim.
const backlogPromptLine = "`tallpine backlog ls` / `tallpine backlog show <ref>`: the project's backlog. Read it only when the human asks about ideas, next work or follow-ups. Put work you're deferring there with `tallpine backlog add`, never in your harness task list. Never start a held item unprompted."

func TestOrchestratorPromptCarriesTheBacklogLineExactlyOnce(t *testing.T) {
	project := promptProject{ID: "tp", Name: "Tallpine"}
	orchestrator := buildSystemPromptText(systemPromptConfig{Role: sessionPromptRoleOrchestrator, Project: project})
	if n := strings.Count(orchestrator, backlogPromptLine); n != 1 {
		t.Fatalf("orchestrator prompt has the backlog line %d times, want 1", n)
	}
	// Orchestrator prompt only: no worker variant mentions the backlog commands.
	for name, cfg := range map[string]systemPromptConfig{
		"worker":            {Role: sessionPromptRoleWorker, Project: project, OrchestratorSessionID: "tp-orch"},
		"worker, no orch":   {Role: sessionPromptRoleWorker, Project: project},
		"standalone worker": {Role: sessionPromptRoleWorker, Standalone: true},
	} {
		if got := buildSystemPromptText(cfg); strings.Contains(got, "tallpine backlog") {
			t.Errorf("%s prompt mentions the backlog commands", name)
		}
	}
}

func TestWorkerTaskPromptGetsBacklogContextAsTaskText(t *testing.T) {
	ctx := "Backlog item B12: Fix leak\n\nNote:\ndetails\n\nDone when:\ntests pass"
	withPrompt := buildTaskPrompt(taskPromptConfig{Role: sessionPromptRoleWorker, Prompt: "Do the work.", BacklogContext: ctx})
	if !strings.HasPrefix(withPrompt, "Do the work.\n\n## Backlog item\n") || !strings.Contains(withPrompt, ctx) {
		t.Fatalf("task prompt = %q", withPrompt)
	}
	promptless := buildTaskPrompt(taskPromptConfig{Role: sessionPromptRoleWorker, BacklogContext: ctx})
	if !strings.HasPrefix(promptless, "Work on this backlog item.") || !strings.Contains(promptless, "tests pass") {
		t.Fatalf("promptless task prompt = %q", promptless)
	}
	// Alongside an issue the issue prompt stays first.
	both := buildTaskPrompt(taskPromptConfig{Role: sessionPromptRoleWorker, IssueID: "42", BacklogContext: ctx})
	if !strings.HasPrefix(both, "Work on issue 42.") || !strings.HasSuffix(both, backlogCloseInstruction) || !strings.Contains(both, "tests pass") {
		t.Fatalf("issue + backlog prompt = %q", both)
	}
	// Orchestrators never get it, and without a context nothing changes.
	if got := buildTaskPrompt(taskPromptConfig{Role: sessionPromptRoleOrchestrator, Prompt: "p", BacklogContext: ctx}); got != "p" {
		t.Fatalf("orchestrator task prompt = %q", got)
	}
	if got := buildTaskPrompt(taskPromptConfig{Role: sessionPromptRoleWorker, Prompt: "p"}); got != "p" {
		t.Fatalf("plain task prompt = %q", got)
	}
}

func TestBacklogWorkerPromptCarriesTheClosesConvention(t *testing.T) {
	ctx := "Backlog item B12: Fix leak\n\nDone when:\ntests pass"
	for name, prompt := range map[string]string{"with prompt": "Do the work.", "promptless": ""} {
		got := buildTaskPrompt(taskPromptConfig{Role: sessionPromptRoleWorker, Prompt: prompt, BacklogContext: ctx})
		for _, want := range []string{"title must contain `closes B<n>`", "only partly meets it, the title must not contain"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: prompt lacks %q", name, want)
			}
		}
	}
	if got := buildTaskPrompt(taskPromptConfig{Role: sessionPromptRoleWorker, Prompt: "Do the work."}); strings.Contains(got, "closes B<n>") {
		t.Errorf("worker without a backlog item got the closes convention")
	}
}
