package sessionmanager

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestWorkerPrompt_DeviationRule(t *testing.T) {
	ctx := context.Background()
	on, off := crossPromptManager(t, false), crossPromptManager(t, false)
	on.SetPlannerEnabled(true)
	const marker = "Deviation:"
	got, err := on.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-1", "", false)
	if err != nil || !strings.Contains(got, marker) {
		t.Fatalf("worker prompt lacks the deviation rule (err %v)", err)
	}
	if got, _ := off.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-2", "", false); strings.Contains(got, marker) {
		t.Error("deviation rule must stay behind the planner switch")
	}
	if got, _ := on.buildSystemPrompt(ctx, domain.KindOrchestrator, "mer", "mer-3", "", false); strings.Contains(got, "## Approved Plans") {
		t.Error("orchestrators do not get the worker rule")
	}
}

func TestPlannerPrompt_PRSplitDefaultsToOne(t *testing.T) {
	for _, want := range []string{"the default is ONE PR", "too large to review well", "independently usable", "vertical slices"} {
		if !strings.Contains(plannerPrompt, want) {
			t.Errorf("planner prompt lacks PR-split guidance %q", want)
		}
	}
}

func TestPlannerPrompt_UserVisibleRulesAreAssumptions(t *testing.T) {
	for _, want := range []string{"never a silent plan decision", "New, not confirmed by owner", "merge related ones", "At most 20 assumptions", `"scopeCut":true`} {
		if !strings.Contains(plannerPrompt, want) {
			t.Errorf("planner prompt lacks %q", want)
		}
	}
	if strings.Contains(plannerPrompt, "cut minor ones") {
		t.Error("planner prompt must not tell the planner to cut minor assumptions")
	}
	if n := strings.Count(plannerPrompt, "a plan much longer than the spec"); n != 1 {
		t.Errorf("proportion line appears %d times, want 1", n)
	}
}

func TestPlannerPromptRequiresExistingCodeInventory(t *testing.T) {
	for _, want := range []string{"Existing code", "reuses or extends", "second implementation"} {
		if !strings.Contains(plannerPrompt, want) {
			t.Errorf("plannerPrompt missing %q", want)
		}
	}
}

// Plan-in-place puts its brief in the task prompt: the worker system prompt is
// the same with the planner switch on, and has no in-place text (B335).
func TestWorkerSystemPromptHasNoInPlaceText(t *testing.T) {
	on := crossPromptManager(t, false)
	on.SetPlannerEnabled(true)
	got, err := on.buildSystemPrompt(context.Background(), domain.KindWorker, "mer", "mer-9", "", false)
	if err != nil || strings.Contains(got, "Plan in place") || strings.Contains(got, "READ-ONLY") {
		t.Fatalf("worker system prompt carries in-place text (err %v)", err)
	}
}
