package sessionmanager

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// plannerPrompt is a behavioural prompt: moving the plan-format text into
// domain.PlanFormatPrompt (shared with plan-in-place, B335) must not change a byte.
func TestPlannerPromptIsByteIdentical(t *testing.T) {
	const want = "34301b45382c2fef808366e6a9a4c08dc2a982ca69e2db35d5d802fd524e8b9c"
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(plannerPrompt))); got != want {
		t.Fatalf("plannerPrompt sha256 = %s, want %s", got, want)
	}
	if !strings.Contains(plannerPrompt, domain.PlanFormatPrompt) {
		t.Fatal("plannerPrompt must embed the shared plan format")
	}
}

// B484: owner decisions for design/research work go through the plan card only.
func TestPlannerPromptsRouteOwnerDecisionsThroughCard(t *testing.T) {
	for _, want := range []string{"Even when planning mode is off", "only through a planner's card", "Design and research workers feed findings to the planner", "Never ask the owner a list of decision questions"} {
		if !strings.Contains(plannerOrchestratorPrompt, want) {
			t.Errorf("orchestrator prompt missing %q", want)
		}
	}
	if !strings.Contains(plannerWorkerPrompt, "Never hand the owner a question list") {
		t.Error("worker prompt must forbid question lists")
	}
}

func TestPlannerModeRuleAutoPlansOnlyMultiPR(t *testing.T) {
	got := plannerModeRule(domain.PlanningAuto)
	for _, want := range []string{"only work that splits into several PRs", "asks its questions in its report", "Never plan trivial fixes", "\"plan this\"", "\"just do it\""} {
		if !strings.Contains(got, want) {
			t.Errorf("auto rule missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "Plan multi-PR work, schema") {
		t.Errorf("auto rule still has the old criteria: %s", got)
	}
}

func TestPlannerOrchestratorPromptIntroFollowsPlanningMode(t *testing.T) {
	if strings.Contains(plannerOrchestratorPrompt, "Non-trivial backlog work gets") {
		t.Error("orchestrator prompt intro still plans every non-trivial item")
	}
	if !strings.Contains(plannerOrchestratorPrompt, "Backlog work that the planning mode below says to plan") {
		t.Error("orchestrator prompt intro must defer to the planning mode")
	}
}

// B710: a stale plan plus an owner intent to continue never stalls silently.
func TestPlannerOrchestratorPromptStalePlanBranch(t *testing.T) {
	for _, want := range []string{"stale plan meets an owner intent to continue", "tallpine plan replan <ref>", "never stall silently"} {
		if !strings.Contains(plannerOrchestratorPrompt, want) {
			t.Errorf("missing %q", want)
		}
	}
}
