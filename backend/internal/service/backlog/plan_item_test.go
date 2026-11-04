package backlog_test

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
)

// fakeDiff scripts the files that changed on the default branch.
type fakeDiff struct{ changed []string }

func (f *fakeDiff) ChangedSince(context.Context, domain.ProjectID, string) ([]string, error) {
	return f.changed, nil
}

func twoPRSubmit() backlog.PlanSubmitInput {
	in := goodSubmit()
	in.PRs = []domain.PlanPR{
		{Title: "Backend seats", Files: []string{"backend/seats/"}, Tests: "TestSeatShare passes", Produces: []string{"Seat.Share()"}},
		{Title: "Seat UI", Files: []string{"ui/seat.tsx"}, Tests: "UI shows shared seats", Consumes: []string{"Seat.Share()"}},
	}
	return in
}

// approved drives a plan to approved with the given PR split.
func (e *planEnv) approved(in backlog.PlanSubmitInput) domain.BacklogPlan {
	e.t.Helper()
	p := e.mustStart()
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), backlog.PlanQuestionsInput{}); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.plans.Submit(e.ctx, "p1", e.planner(p), e.item.Ref(), in); err != nil {
		e.t.Fatal(err)
	}
	out, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{})
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *planEnv) children() []backlog.View {
	e.t.Helper()
	kids, err := e.bl.List(e.ctx, "p1", backlog.ListFilter{ParentRef: e.item.Ref()})
	if err != nil {
		e.t.Fatal(err)
	}
	return kids
}

func TestApprove_CreatesChildrenOnlyForTwoPlusPRs(t *testing.T) {
	e := newPlanEnv(t, true)
	e.approved(twoPRSubmit())
	kids := e.children()
	if len(kids) != 2 {
		t.Fatalf("children = %d, want 2", len(kids))
	}
	if kids[0].Title != "Backend seats" || kids[1].Title != "Seat UI" || kids[1].DoneWhen != "UI shows shared seats" {
		t.Fatalf("children = %q / %q (done-when %q)", kids[0].Title, kids[1].Title, kids[1].DoneWhen)
	}
	if kids[0].State != domain.BacklogOpen {
		t.Fatalf("child state = %s, want open", kids[0].State)
	}
	second, err := e.bl.Get(e.ctx, "p1", kids[1].Ref())
	if err != nil {
		t.Fatal(err)
	}
	blocked := false
	for _, l := range second.LinkViews {
		if l.Kind == domain.BacklogLinkBlockedBy && l.Display == kids[0].Ref() {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("second child links = %+v, want blocked_by %s", second.LinkViews, kids[0].Ref())
	}
	first, _ := e.bl.Get(e.ctx, "p1", kids[0].Ref())
	for _, l := range first.LinkViews {
		if l.Kind == domain.BacklogLinkBlockedBy {
			t.Fatalf("first child should not be blocked: %+v", first.LinkViews)
		}
	}
}

func TestApprove_OnePRKeepsItemStartable(t *testing.T) {
	e := newPlanEnv(t, true)
	e.approved(goodSubmit())
	if n := len(e.children()); n != 0 {
		t.Fatalf("children = %d, want none for a one-PR plan", n)
	}
	if _, err := e.bl.PrepareSpawn(e.ctx, "p1", e.item.Ref(), false); err != nil {
		t.Fatalf("PrepareSpawn on the planned item = %v, want startable", err)
	}
}

func TestApprove_ChildSpawnGetsParentPlan(t *testing.T) {
	e := newPlanEnv(t, true)
	e.approved(twoPRSubmit())
	kids := e.children()
	got, err := e.bl.PrepareSpawn(e.ctx, "p1", kids[1].Ref(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Context, "each friend") || !strings.Contains(got.Context, "You build PR 2 of 2") {
		t.Fatalf("child context lacks the parent's decisions or its PR entry:\n%s", got.Context)
	}
}

func TestStale_OnlyWhenPlannedPathsChanged(t *testing.T) {
	diff := &fakeDiff{}
	e := newPlanEnvWith(t, true, func(d *backlog.PlanDeps) { d.Diff = diff })
	p := e.approved(goodSubmit()) // names a.go
	// Base commit is empty in tests: give the plan one.
	e.setBase(p.ID, "abc123")
	for _, c := range []struct {
		changed []string
		stale   bool
	}{
		{nil, false},
		{[]string{"other/file.go"}, false},
		{[]string{"a.go"}, true},
	} {
		diff.changed = c.changed
		if got := e.summary().Stale; got != c.stale {
			t.Fatalf("changed %v: stale = %v, want %v", c.changed, got, c.stale)
		}
	}
	// A directory entry covers files below it.
	if !backlog.PlannedPathsChangedForTest([]domain.PlanPR{{Files: []string{"backend/seats/"}}}, []string{"backend/seats/x.go"}) {
		t.Fatal("directory entry should cover its files")
	}
	// A plan that names no files has nothing to compare: never stale (B481).
	if backlog.PlannedPathsChangedForTest([]domain.PlanPR{{Title: "x"}}, []string{"z"}) {
		t.Fatal("plan naming no files must not be stale")
	}
}

func TestStale_BuildAnywayRecorded(t *testing.T) {
	diff := &fakeDiff{changed: []string{"a.go"}}
	e := newPlanEnvWith(t, true, func(d *backlog.PlanDeps) { d.Diff = diff })
	p := e.approved(goodSubmit())
	e.setBase(p.ID, "abc123")
	if !e.summary().Stale {
		t.Fatal("plan should be stale")
	}
	_, err := e.bl.PrepareSpawn(e.ctx, "p1", e.item.Ref(), false)
	if apiCode(t, err) != "PLAN_STALE" {
		t.Fatalf("PrepareSpawn on a stale plan = %v, want PLAN_STALE", err)
	}
	if _, err := e.plans.BuildAnyway(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
	if e.summary().Stale {
		t.Fatal("a plan built anyway is not stale")
	}
	if _, err := e.bl.PrepareSpawn(e.ctx, "p1", e.item.Ref(), false); err != nil {
		t.Fatalf("PrepareSpawn after build anyway = %v", err)
	}
}

func TestItemEditedMidPlanFlagged(t *testing.T) {
	e := newPlanEnv(t, true)
	e.mustStart()
	if e.summary().ItemChanged {
		t.Fatal("fresh plan should not be flagged")
	}
	hold := "waiting"
	if _, err := e.bl.Update(e.ctx, "p1", e.item.Ref(), backlog.UpdateInput{Hold: &hold}); err != nil {
		t.Fatal(err)
	}
	if e.summary().ItemChanged {
		t.Fatal("a hold change must not flag the plan")
	}
	note := "also let friends split"
	if _, err := e.bl.Update(e.ctx, "p1", e.item.Ref(), backlog.UpdateInput{Note: &note}); err != nil {
		t.Fatal(err)
	}
	if !e.summary().ItemChanged {
		t.Fatal("a note edit must flag the plan")
	}
	v, err := e.bl.Get(e.ctx, "p1", e.item.Ref())
	if err != nil || v.Plan == nil || !v.Plan.ItemChanged {
		t.Fatalf("item view plan = %+v, err %v; want itemChanged", v.Plan, err)
	}
}

func TestItemDroppedSupersedesPlan(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.mustStart()
	if _, err := e.bl.SetState(e.ctx, "p1", e.item.Ref(), domain.BacklogDropped, "not needed"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := e.store.GetBacklogPlan(e.ctx, p.ID)
	if err != nil || !ok || got.Status != domain.PlanSuperseded {
		t.Fatalf("plan = %+v ok %v err %v, want superseded", got.Status, ok, err)
	}
	if len(e.sessions.terminated) != 1 {
		t.Fatalf("terminated planners = %d, want 1", len(e.sessions.terminated))
	}
}

func TestPrepareSpawn_PlanFirstWhenModeAlways(t *testing.T) {
	e := newPlanEnvWith(t, true, func(d *backlog.PlanDeps) {
		d.Mode = func(context.Context, domain.ProjectID) domain.PlanningMode { return domain.PlanningAlways }
	})
	_, err := e.bl.PrepareSpawn(e.ctx, "p1", e.item.Ref(), false)
	if apiCode(t, err) != "PLAN_REQUIRED" || !strings.Contains(err.Error(), "plan first") {
		t.Fatalf("PrepareSpawn without a plan = %v, want plan first", err)
	}
	if _, err := e.bl.PrepareSpawn(e.ctx, "p1", e.item.Ref(), true); err != nil {
		t.Fatalf("forced PrepareSpawn = %v, want ok", err)
	}
	e.mustStart()
	_, err = e.bl.PrepareSpawn(e.ctx, "p1", e.item.Ref(), false)
	if apiCode(t, err) != "PLAN_REQUIRED" || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("PrepareSpawn with an open plan = %v, want not approved", err)
	}
}

func TestPrepareSpawn_AutoModeStartsWithoutPlan(t *testing.T) {
	e := newPlanEnv(t, true)
	if _, err := e.bl.PrepareSpawn(e.ctx, "p1", e.item.Ref(), false); err != nil {
		t.Fatalf("auto mode must not require a plan: %v", err)
	}
}

func TestPrepareSpawn_PlannerOffIgnoresMode(t *testing.T) {
	e := newPlanEnvWith(t, false, func(d *backlog.PlanDeps) {
		d.Mode = func(context.Context, domain.ProjectID) domain.PlanningMode { return domain.PlanningAlways }
	})
	if _, err := e.bl.PrepareSpawn(e.ctx, "p1", e.item.Ref(), false); err != nil {
		t.Fatalf("planner off: %v", err)
	}
}

func (e *planEnv) summary() *backlog.PlanSummary {
	e.t.Helper()
	s := e.plans.Summary(e.ctx, e.item.ID, true)
	if s == nil {
		e.t.Fatal("no plan summary")
	}
	return s
}

// setBase gives a plan a base commit, as the daemon's resolver would.
func (e *planEnv) setBase(id, commit string) {
	e.t.Helper()
	if _, err := e.store.UpdateBacklogPlan(e.ctx, id, func(p *domain.BacklogPlan) error { p.BaseCommit = commit; return nil }); err != nil {
		e.t.Fatal(err)
	}
}

func TestStale_ChildOnlyByItsOwnFiles(t *testing.T) {
	diff := &fakeDiff{changed: []string{"backend/seats/x.go"}} // child 1's files merged
	e := newPlanEnvWith(t, true, func(d *backlog.PlanDeps) { d.Diff = diff })
	p := e.approved(twoPRSubmit())
	e.setBase(p.ID, "abc123")
	kids := e.children()
	if _, err := e.bl.PrepareSpawn(e.ctx, "p1", kids[1].Ref(), false); err != nil {
		t.Fatalf("child 2 after child 1 merged = %v, want startable", err)
	}
	diff.changed = []string{"ui/seat.tsx"}
	_, err := e.bl.PrepareSpawn(e.ctx, "p1", kids[1].Ref(), false)
	if apiCode(t, err) != "PLAN_STALE" {
		t.Fatalf("child 2 with its own file changed = %v, want PLAN_STALE", err)
	}
}

func TestChild_MatchedByKeyNotTitle(t *testing.T) {
	diff := &fakeDiff{changed: []string{"backend/seats/x.go"}}
	e := newPlanEnvWith(t, true, func(d *backlog.PlanDeps) { d.Diff = diff })
	in := twoPRSubmit()
	in.PRs[0].Title, in.PRs[1].Title = "Same title", "Same title"
	p := e.approved(in)
	e.setBase(p.ID, "abc123")
	kids := e.children()
	renamed := "Renamed by the owner"
	if _, err := e.bl.Update(e.ctx, "p1", kids[1].Ref(), backlog.UpdateInput{Title: &renamed}); err != nil {
		t.Fatal(err)
	}
	got, err := e.bl.PrepareSpawn(e.ctx, "p1", kids[1].Ref(), false)
	if err != nil {
		t.Fatalf("child 2 (renamed, duplicate planned titles) = %v, want startable", err)
	}
	if !strings.Contains(got.Context, "You build PR 2 of 2") {
		t.Fatalf("context lacks its PR entry:\n%s", got.Context)
	}
}

// replanTo re-plans the item and approves the new split.
func (e *planEnv) replanTo(prs []domain.PlanPR) {
	e.t.Helper()
	if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		e.t.Fatal(err)
	}
	p, _, _ := e.store.OpenBacklogPlanForItem(e.ctx, e.item.ID)
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), backlog.PlanQuestionsInput{}); err != nil {
		e.t.Fatal(err)
	}
	in := goodSubmit()
	in.PRs = prs
	if _, err := e.plans.Submit(e.ctx, "p1", e.planner(p), e.item.Ref(), in); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		e.t.Fatal(err)
	}
}

func prs(titles ...string) []domain.PlanPR {
	var out []domain.PlanPR
	for _, t := range titles {
		out = append(out, domain.PlanPR{Title: t, Files: []string{t + "/"}, Tests: "Test" + t, Produces: []string{t + "()"}})
	}
	return out
}

// live returns the open children's titles in order, and all children's states.
func (e *planEnv) liveTitles() []string {
	var out []string
	for _, k := range e.children() {
		if k.State == domain.BacklogOpen {
			out = append(out, k.Title+"#"+k.Fields["plan.pr"])
		}
	}
	return out
}

func eq(a []string, b ...string) bool { return strings.Join(a, "|") == strings.Join(b, "|") }

func approvedWith(t *testing.T, titles ...string) *planEnv {
	e := newPlanEnv(t, true)
	in := goodSubmit()
	in.PRs = prs(titles...)
	e.approved(in)
	return e
}

func TestReplan_MorePRsDropsOldUnstartedAndMakesNew(t *testing.T) {
	e := approvedWith(t, "A", "B")
	old := e.children()
	e.replanTo(prs("A", "B", "C"))
	if got := e.liveTitles(); !eq(got, "A#1", "B#2", "C#3") {
		t.Fatalf("live children = %v", got)
	}
	for _, k := range old {
		v, _ := e.bl.Get(e.ctx, "p1", k.Ref())
		if v.State != domain.BacklogDropped || v.ClosedReason != "superseded by re-plan" {
			t.Fatalf("old child %s = %s %q, want dropped, superseded by re-plan", k.Ref(), v.State, v.ClosedReason)
		}
	}
}

func TestReplan_FewerPRsAndReorder(t *testing.T) {
	e := approvedWith(t, "A", "B", "C")
	e.replanTo(prs("C", "A"))
	if got := e.liveTitles(); !eq(got, "C#1", "A#2") {
		t.Fatalf("after reorder+fewer = %v", got)
	}
	kids := e.children()
	var cur []backlog.View
	for _, k := range kids {
		if k.State == domain.BacklogOpen {
			cur = append(cur, k)
		}
	}
	second, _ := e.bl.Get(e.ctx, "p1", cur[1].Ref())
	blocked := false
	for _, l := range second.LinkViews {
		if l.Kind == domain.BacklogLinkBlockedBy && l.Display == cur[0].Ref() {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("new chain missing: %+v", second.LinkViews)
	}
}

func TestReplan_ToOnePRWithNothingKeptMakesItemStartableAgain(t *testing.T) {
	e := approvedWith(t, "A", "B")
	e.replanTo(prs("A"))
	if n := len(e.children()); n != 0 {
		t.Fatalf("children = %d, want none", n)
	}
	if _, err := e.bl.PrepareSpawn(e.ctx, "p1", e.item.Ref(), false); err != nil {
		t.Fatalf("item should be startable again: %v", err)
	}
}

func TestReplan_KeepsStartedAndDoneChildren(t *testing.T) {
	diff := &fakeDiff{changed: []string{"A/x.go"}}
	e := newPlanEnvWith(t, true, func(d *backlog.PlanDeps) { d.Diff = diff })
	in := goodSubmit()
	in.PRs = prs("A", "B", "C")
	p := e.approved(in)
	e.setBase(p.ID, "abc123")
	kids := e.children()
	if err := e.bl.LinkSession(e.ctx, kids[0].ID, "sess-x", "human"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.bl.SetState(e.ctx, "p1", kids[1].Ref(), domain.BacklogDone, "merged"); err != nil {
		t.Fatal(err)
	}
	e.replanTo(prs("D"))
	if n := len(e.sessions.spawned); n < 2 {
		t.Fatalf("spawned = %d", n)
	}
	brief := e.sessions.spawned[len(e.sessions.spawned)-1].Prompt
	if !strings.Contains(brief, kids[0].Ref()+" A (in progress)") || !strings.Contains(brief, kids[1].Ref()+" B (done)") || strings.Contains(brief, " C (") {
		t.Fatalf("re-planner brief lacks the kept children:\n%s", brief)
	}
	byRef := map[string]backlog.View{}
	for _, k := range e.children() {
		byRef[k.Ref()] = k
	}
	if byRef[kids[0].Ref()].State != domain.BacklogOpen || byRef[kids[0].Ref()].Fields["plan.pr"] != "" {
		t.Fatalf("started child = %+v, want open with plan.pr cleared", byRef[kids[0].Ref()])
	}
	if byRef[kids[1].Ref()].State != domain.BacklogDone || byRef[kids[2].Ref()].State != domain.BacklogDropped {
		t.Fatalf("done/unstarted = %s/%s", byRef[kids[1].Ref()].State, byRef[kids[2].Ref()].State)
	}
	if got := e.liveTitles(); !eq(got, "A#", "D#1") {
		t.Fatalf("live = %v, want kept A and new D", got)
	}
	// The kept child gets no stale refusal and no PR line from the new plan.
	got, err := e.bl.PrepareSpawn(e.ctx, "p1", kids[0].Ref(), false)
	if err != nil || strings.Contains(got.Context, "You build PR") {
		t.Fatalf("kept child spawn = %v / %q", err, got.Context)
	}
}

func TestStale_ParentWithChildrenNotStaleWhenChildOneMerged(t *testing.T) {
	diff := &fakeDiff{changed: []string{"backend/seats/x.go"}}
	e := newPlanEnvWith(t, true, func(d *backlog.PlanDeps) { d.Diff = diff })
	p := e.approved(twoPRSubmit())
	e.setBase(p.ID, "abc123")
	if e.summary().Stale {
		t.Fatal("a plan split into children is checked per child, not as a whole")
	}
}

func TestUndoPlanChildren_ReopensAutoClosedParent(t *testing.T) {
	e := newPlanEnv(t, true)
	// Children made for an approval that then failed: undo must leave the item open.
	kids := make([]string, 0, 2)
	for _, title := range []string{"A", "B"} {
		v, err := e.bl.Create(e.ctx, "p1", owner, backlog.CreateInput{Title: title, ParentRef: e.item.Ref()})
		if err != nil {
			t.Fatal(err)
		}
		kids = append(kids, v.ID)
	}
	backlog.UndoPlanChildrenForTest(e.ctx, e.bl, e.item.BacklogItem, kids)
	got, err := e.bl.Get(e.ctx, "p1", e.item.Ref())
	if err != nil || got.State != domain.BacklogOpen || got.ChildCount != 0 {
		t.Fatalf("item = %s children %d err %v, want open with no children", got.State, got.ChildCount, err)
	}
}

func TestApprove_OrchestratorBadOwnerMessageLeavesNoChildren(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.mustStart()
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), backlog.PlanQuestionsInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.Submit(e.ctx, "p1", e.planner(p), e.item.Ref(), twoPRSubmit()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.Approve(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{OwnerMessage: "nope"}); err == nil {
		t.Fatal("approve with a bad owner message should fail")
	}
	got, _ := e.bl.Get(e.ctx, "p1", e.item.Ref())
	if got.State != domain.BacklogOpen || got.ChildCount != 0 {
		t.Fatalf("item = %s with %d children, want open and none", got.State, got.ChildCount)
	}
}
