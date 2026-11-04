package backlog

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestNarrowedForChild(t *testing.T) {
	plan := domain.BacklogPlan{PRSplit: []domain.PlanPR{{Title: "a"}, {Title: "b"}}}
	if got := narrowedForChild(plan, false, 0, false); len(got.PRSplit) != 2 || got.BuildAnyway {
		t.Fatalf("owner item must check the whole plan, got %+v", got)
	}
	got := narrowedForChild(plan, true, 1, true)
	if len(got.PRSplit) != 1 || got.PRSplit[0].Title != "b" || got.BuildAnyway {
		t.Fatalf("child must check only its own entry, got %+v", got)
	}
	if got := narrowedForChild(plan, true, 0, false); !got.BuildAnyway || len(plan.PRSplit) != 2 {
		t.Fatalf("child without an entry must skip the stale check without mutating the plan, got %+v", got)
	}
}
