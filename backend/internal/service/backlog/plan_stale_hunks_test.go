package backlog_test

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
)

// hunkDiff also scripts the changed lines per path.
type hunkDiff struct {
	fakeDiff
	lines map[string][]string
}

func (h *hunkDiff) ChangedLines(context.Context, domain.ProjectID, string, []string) (map[string][]string, error) {
	return h.lines, nil
}

func TestStale_HunkLevel(t *testing.T) {
	for _, c := range []struct {
		name  string
		lines []string
		stale bool
	}{
		{"unrelated hunk", []string{"type SpawnDTO struct {", "// moved"}, false},
		{"hunk edits a planned symbol", []string{"func (s *Service) planWidget() {"}, true},
		{"body edit, header names planned func", []string{" func (s *Service) planWidget() {", "return 1", "return 2"}, true},
		{"pointer write is code, not a comment", []string{"*p = planWidget()"}, true},
	} {
		d := &hunkDiff{fakeDiff: fakeDiff{changed: []string{"a.go"}}, lines: map[string][]string{"a.go": c.lines}}
		e := newPlanEnvWith(t, true, func(pd *backlog.PlanDeps) { pd.Diff = d })
		in := goodSubmit()
		in.PRs = []domain.PlanPR{{Title: "Change `planWidget`", Files: []string{"a.go"}, Tests: "TestX", Produces: []string{"W"}}}
		p := e.approved(in)
		e.setBase(p.ID, "abc123")
		if got := e.summary().Stale; got != c.stale {
			t.Fatalf("%s: stale = %v, want %v", c.name, got, c.stale)
		}
	}
}

func TestStale_NoSymbolsFailsClosed(t *testing.T) {
	d := &hunkDiff{fakeDiff: fakeDiff{changed: []string{"a.go"}}, lines: map[string][]string{"a.go": {"return 2"}}}
	e := newPlanEnvWith(t, true, func(pd *backlog.PlanDeps) { pd.Diff = d })
	in := goodSubmit()
	in.PRs = []domain.PlanPR{{Title: "Add retry to spawn", Files: []string{"a.go"}, Tests: "retries", Produces: []string{"retry"}}}
	p := e.approved(in)
	e.setBase(p.ID, "abc123")
	if !e.summary().Stale {
		t.Fatal("plan naming files but no symbols must stay file-level stale")
	}
}

func TestStale_BackticksPathIsNotSymbol(t *testing.T) {
	d := &hunkDiff{fakeDiff: fakeDiff{changed: []string{"a.go"}}, lines: map[string][]string{"a.go": {`"tallpine.eu/tallpine/backend/internal/domain"`}}}
	e := newPlanEnvWith(t, true, func(pd *backlog.PlanDeps) { pd.Diff = d })
	in := goodSubmit()
	in.Plan = "Edit `backend/internal/cli/spawn.go` and `a.go`, function `planWidget`."
	in.PRs = []domain.PlanPR{{Title: "Change `planWidget`", Files: []string{"a.go"}, Tests: "TestX", Produces: []string{"W"}}}
	p := e.approved(in)
	e.setBase(p.ID, "abc123")
	if e.summary().Stale {
		t.Fatal("an import-line change must not match words from backticked paths")
	}
}
