package backlog_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
)

// movingBase reports a default-branch head that tests advance.
type movingBase struct {
	mu   sync.Mutex
	head string
}

func (m *movingBase) Base(context.Context, domain.ProjectID) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.head, "main", nil
}

// sinceDiff reports changes only for the commit the plan is based on.
type sinceDiff struct {
	changedSince map[string][]string
	lines        map[string][]string
	linesErr     error
}

func (d *sinceDiff) ChangedSince(_ context.Context, _ domain.ProjectID, base string) ([]string, error) {
	return d.changedSince[base], nil
}

func (d *sinceDiff) ChangedLines(context.Context, domain.ProjectID, string, []string) (map[string][]string, error) {
	return d.lines, d.linesErr
}

// B481: merges that landed while the planner worked are what the plan was
// written against; the baseline is the commit at submit, not at start.
func TestStale_BaselineIsSubmitCommit(t *testing.T) {
	base := &movingBase{head: "start"}
	diff := &sinceDiff{changedSince: map[string][]string{"start": {"a.go"}}}
	e := newPlanEnvWith(t, true, func(d *backlog.PlanDeps) { d.Base, d.Diff = base, diff })
	pl := e.mustStart()
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(pl), e.item.Ref(), backlog.PlanQuestionsInput{}); err != nil {
		t.Fatal(err)
	}
	base.head = "submit" // the prerequisite merged while planning
	if _, err := e.plans.Submit(e.ctx, "p1", e.planner(pl), e.item.Ref(), goodSubmit()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
	if e.summary().Stale {
		t.Fatal("a merge before the plan was submitted must not make it stale")
	}
	diff.changedSince["submit"] = []string{"a.go"}
	if !e.summary().Stale {
		t.Fatal("a merge after submit that touches a planned file must be stale")
	}
}

func TestStale_HunkDiffErrorIsNotStale(t *testing.T) {
	d := &sinceDiff{changedSince: map[string][]string{"abc123": {"a.go"}}, linesErr: errors.New("git failed")}
	e := newPlanEnvWith(t, true, func(pd *backlog.PlanDeps) { pd.Diff = d })
	in := goodSubmit()
	in.PRs = []domain.PlanPR{{Title: "Change `planWidget`", Files: []string{"a.go"}, Tests: "TestX", Produces: []string{"W"}}}
	p := e.approved(in)
	e.setBase(p.ID, "abc123")
	if e.summary().Stale {
		t.Fatal("a failed hunk diff is advisory and must not mark the plan stale")
	}
}
