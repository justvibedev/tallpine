package backlog_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

type runPlanEnv struct {
	runEnv
	plans    *backlog.PlanService
	sessions *fakeSessions
}

func newRunPlanEnv(t *testing.T, mode domain.PlanningMode) runPlanEnv {
	t.Helper()
	s := sqlitetest.MustOpen(t)
	ctx := context.Background()
	if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "p1", Path: "/tmp/p1", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	ps := &planStore{Store: s}
	sessions := &fakeSessions{store: s, ended: map[domain.SessionID]bool{}}
	bl := backlog.New(backlog.Deps{Store: s})
	plans := backlog.NewPlanService(backlog.PlanDeps{
		Backlog: bl, Store: ps, Sessions: sessions, Notify: &planNotifier{}, Enabled: true,
		Mode: func(context.Context, domain.ProjectID) domain.PlanningMode { return mode },
	})
	n := &fakeNotifier{}
	runs := backlog.NewRunService(backlog.RunDeps{Store: s, Notify: n, Enabled: true, Plans: plans})
	return runPlanEnv{runEnv: runEnv{runs: runs, items: bl, notify: n, store: s}, plans: plans, sessions: sessions}
}

func (e runPlanEnv) runItem(t *testing.T, run, ref string) backlog.RunItemView {
	t.Helper()
	v, err := e.runs.Get(context.Background(), "p1", run)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range v.Items {
		if it.ItemRef() == ref {
			return it
		}
	}
	t.Fatalf("%s not in run %s", ref, run)
	return backlog.RunItemView{}
}

// approve drives the item's plan through questions, defaults, submit and approve.
func (e runPlanEnv) approve(t *testing.T, ref string) { e.approveWith(t, ref, goodSubmit()) }

func (e runPlanEnv) approveWith(t *testing.T, ref string, sub backlog.PlanSubmitInput) {
	t.Helper()
	ctx := context.Background()
	pl, ok, err := e.plans.Current(ctx, e.itemID(t, ref))
	if err != nil || !ok {
		t.Fatalf("no plan for %s: %v", ref, err)
	}
	planner := backlog.Actor{Kind: domain.BacklogOriginWorker, SessionID: string(pl.PlannerSessionID)}
	if _, err := e.plans.SubmitQuestions(ctx, "p1", planner, ref, goodQuestions()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.Defaults(ctx, "p1", human, ref, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.Submit(ctx, "p1", planner, ref, sub); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.Approve(ctx, "p1", human, ref, backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
}

func (e runPlanEnv) itemID(t *testing.T, ref string) string {
	t.Helper()
	seq, _ := domain.ParseBacklogRef(ref)
	it, ok, err := e.store.GetBacklogItemByRef(context.Background(), "p1", seq)
	if err != nil || !ok {
		t.Fatalf("item %s: %v", ref, err)
	}
	return it.ID
}

func TestRun_PlansRequestedForUnplannedItems(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningAlways)
	a, b := e.item(t, "A"), e.item(t, "B")
	run := e.started(t, a, b)
	if len(e.sessions.spawned) != 2 {
		t.Fatalf("planners spawned = %d, want 2", len(e.sessions.spawned))
	}
	for _, ref := range []string{a, b} {
		it := e.runItem(t, run, ref)
		if it.Status != domain.BacklogRunItemNeedsYou || it.Reason != domain.BacklogRunReasonPlanPending || it.Question == "" {
			t.Errorf("%s = %s/%q/%q, want needs_you plan_pending with a question", ref, it.Status, it.Reason, it.Question)
		}
		if it.PlanStatus != domain.PlanDrafting {
			t.Errorf("%s plan status = %q, want drafting", ref, it.PlanStatus)
		}
	}
}

func TestRun_ItemStartsWhenOwnPlanApproved(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningAlways)
	a, b := e.item(t, "A"), e.item(t, "B")
	run := e.startedLanes(t, []string{a}, []string{b})
	ctx := context.Background()
	if _, err := e.runs.SetItem(ctx, "p1", orch, run, a, backlog.RunItemUpdate{Status: domain.BacklogRunItemActive}); err == nil {
		t.Fatal("an item whose plan is not approved was started")
	}
	e.approve(t, a)
	it := e.runItem(t, run, a)
	if it.Status != domain.BacklogRunItemQueued || it.Reason != "" || it.Question != "" || it.PlanStatus != domain.PlanApproved {
		t.Fatalf("approved item = %s/%q/%q plan %q, want queued, no reason, approved", it.Status, it.Reason, it.Question, it.PlanStatus)
	}
	if _, err := e.runs.SetItem(ctx, "p1", orch, run, a, backlog.RunItemUpdate{Status: domain.BacklogRunItemActive}); err != nil {
		t.Fatalf("start after approval: %v", err)
	}
}

func TestRun_NoRunWideGate(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningAlways)
	a, b := e.item(t, "A"), e.item(t, "B")
	run := e.startedLanes(t, []string{a}, []string{b})
	e.approve(t, a)
	// B's plan has not even asked its questions, yet A builds.
	if _, err := e.runs.SetItem(context.Background(), "p1", orch, run, a, backlog.RunItemUpdate{Status: domain.BacklogRunItemActive}); err != nil {
		t.Fatalf("A must start while B still plans: %v", err)
	}
	if got := e.runItem(t, run, b); got.Reason != domain.BacklogRunReasonPlanPending {
		t.Fatalf("B = %s/%q, want it still waiting for its plan", got.Status, got.Reason)
	}
	if v, _ := e.runs.Get(context.Background(), "p1", run); v.Status == domain.BacklogRunDone {
		t.Fatal("a run whose items all wait for plans must stay open")
	}
}

func TestRun_ModeOffSkipsPlanning(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningOff)
	a := e.item(t, "A")
	run := e.started(t, a)
	if len(e.sessions.spawned) != 0 {
		t.Fatalf("planners spawned with mode off: %d", len(e.sessions.spawned))
	}
	if it := e.runItem(t, run, a); it.Status != domain.BacklogRunItemQueued || it.Reason != "" {
		t.Fatalf("item = %s/%q, want plain queued", it.Status, it.Reason)
	}
	if _, err := e.runs.SetItem(context.Background(), "p1", orch, run, a, backlog.RunItemUpdate{Status: domain.BacklogRunItemActive}); err != nil {
		t.Fatalf("start with mode off: %v", err)
	}
}

func TestRun_PlannersRespectWorkerCap(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningAlways)
	var refs []string
	for i := 0; i < domain.MaxBacklogRunWorkers+2; i++ {
		refs = append(refs, e.item(t, "item"))
	}
	run := e.started(t, refs...)
	if len(e.sessions.spawned) != domain.MaxBacklogRunWorkers {
		t.Fatalf("planners = %d, want the cap %d", len(e.sessions.spawned), domain.MaxBacklogRunWorkers)
	}
	last := refs[len(refs)-1] // two items overflow; approving one plan frees exactly one slot
	if it := e.runItem(t, run, last); it.Status != domain.BacklogRunItemQueued || it.Reason != domain.BacklogRunReasonPlanPending {
		t.Fatalf("overflow item = %s/%q, want queued plan_pending", it.Status, it.Reason)
	}
	e.approve(t, refs[0]) // frees a slot
	if len(e.sessions.spawned) != domain.MaxBacklogRunWorkers+1 {
		t.Fatalf("planners = %d, want the freed slot used", len(e.sessions.spawned))
	}
	if it := e.runItem(t, run, refs[len(refs)-2]); it.Status != domain.BacklogRunItemNeedsYou {
		t.Fatalf("first overflow item = %s, want needs_you once its planner started", it.Status)
	}
	if it := e.runItem(t, run, last); it.Status != domain.BacklogRunItemQueued {
		t.Fatalf("second overflow item = %s, want it still waiting for a slot", it.Status)
	}
}

func TestRun_AutoModePlansOnlyMarkedItems(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningAuto)
	marked, plain := e.item(t, "marked"), e.item(t, "plain")
	if _, _, err := e.plans.Start(context.Background(), "p1", human, marked); err != nil { // "Plan now" before the run
		t.Fatal(err)
	}
	before := len(e.sessions.spawned)
	run := e.started(t, marked, plain)
	if len(e.sessions.spawned) != before {
		t.Fatalf("auto mode started %d new planners, want none", len(e.sessions.spawned)-before)
	}
	if it := e.runItem(t, run, marked); it.Status != domain.BacklogRunItemNeedsYou || it.Reason != domain.BacklogRunReasonPlanPending {
		t.Fatalf("marked = %s/%q, want it to wait for its plan", it.Status, it.Reason)
	}
	if it := e.runItem(t, run, plain); it.Status != domain.BacklogRunItemQueued || it.Reason != "" {
		t.Fatalf("plain = %s/%q, want an ordinary queued item", it.Status, it.Reason)
	}
}

func TestRun_ApprovedSplitReplacesParentWithChildren(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningAlways)
	a, b := e.item(t, "A"), e.item(t, "B")
	run := e.started(t, a, b)
	sub := goodSubmit()
	sub.PRs = []domain.PlanPR{
		{Title: "First", Files: []string{"a.go"}, Tests: "TestOne", Produces: []string{"One()"}},
		{Title: "Second", Files: []string{"b.go"}, Tests: "TestTwo", Consumes: []string{"One()"}},
	}
	e.approveWith(t, a, sub)
	v, err := e.runs.Get(context.Background(), "p1", run)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range v.Items {
		got = append(got, it.ItemTitle)
		if it.ItemTitle == "First" || it.ItemTitle == "Second" {
			if it.Status != domain.BacklogRunItemQueued || it.Lane != 0 {
				t.Errorf("%s = %s lane %d, want queued in the parent's lane", it.ItemTitle, it.Status, it.Lane)
			}
		}
	}
	want := []string{"First", "Second", "B"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("run items = %v, want %v (children in plan order, parent gone)", got, want)
	}
	ctx := context.Background()
	if _, err := e.runs.SetItem(ctx, "p1", orch, run, "B4", backlog.RunItemUpdate{Status: domain.BacklogRunItemActive}); err == nil {
		t.Fatal("the second child started before the first (blocked_by chain)")
	}
	if _, err := e.runs.SetItem(ctx, "p1", orch, run, "B3", backlog.RunItemUpdate{Status: domain.BacklogRunItemActive}); err != nil {
		t.Fatal(err)
	}
}

func TestRun_FreedWorkerSlotStartsWaitingPlanner(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningAlways)
	ctx := context.Background()
	var refs []string
	var lanes [][]string
	for i := 0; i < domain.MaxBacklogRunWorkers+2; i++ {
		r := e.item(t, "item")
		refs = append(refs, r)
		lanes = append(lanes, []string{r})
	}
	run := e.startedLanes(t, lanes...)
	for _, r := range refs[:domain.MaxBacklogRunWorkers] {
		e.approve(t, r)
		if _, err := e.runs.SetItem(ctx, "p1", orch, run, r, backlog.RunItemUpdate{Status: domain.BacklogRunItemActive}); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(e.sessions.spawned); got != domain.MaxBacklogRunWorkers+1 {
		t.Fatalf("planners = %d, want %d (the last item waits: workers and a plan fill the cap)", got, domain.MaxBacklogRunWorkers+1)
	}
	for _, r := range refs[:2] {
		if _, err := e.runs.SetItem(ctx, "p1", orch, run, r, backlog.RunItemUpdate{Status: domain.BacklogRunItemLanded}); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(e.sessions.spawned); got != domain.MaxBacklogRunWorkers+2 {
		t.Fatalf("planners = %d, want the freed slot to start the last item's planner", got)
	}
}

func TestRun_QueuedRewriteCannotSkipThePlan(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningAlways)
	a := e.item(t, "A")
	run := e.started(t, a)
	ctx := context.Background()
	if _, err := e.runs.SetItem(ctx, "p1", orch, run, a, backlog.RunItemUpdate{Status: domain.BacklogRunItemQueued}); err != nil {
		t.Fatal(err)
	}
	_, aerr := e.runs.SetItem(ctx, "p1", orch, run, a, backlog.RunItemUpdate{Status: domain.BacklogRunItemActive})
	if aerr == nil {
		t.Fatal("rewriting the status to queued let an unplanned item start")
	}
}

func TestRun_PlannersAreLinkedAsTheRunActor(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningAlways)
	a := e.item(t, "A")
	run := e.started(t, a)
	it, ok, err := e.store.GetBacklogItem(context.Background(), e.itemID(t, a))
	if err != nil || !ok {
		t.Fatal(err)
	}
	found := false
	for _, l := range it.Links {
		if l.Kind == domain.BacklogLinkSession {
			found = true
			if l.CreatedBy != "run:"+run {
				t.Errorf("planner linked by %q, want run:%s", l.CreatedBy, run)
			}
		}
	}
	if !found {
		t.Fatal("planner session not linked to the item")
	}
}

func TestRun_FailedPlanKeepsRunOpenAndItemWaiting(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningAlways)
	a := e.item(t, "A")
	run := e.started(t, a)
	ctx := context.Background()
	pl, _, _ := e.plans.Current(ctx, e.itemID(t, a))
	if !e.plans.SessionEnded(ctx, pl.PlannerSessionID) {
		t.Fatal("planner end not handled")
	}
	it := e.runItem(t, run, a)
	if it.Status != domain.BacklogRunItemNeedsYou || it.Reason != domain.BacklogRunReasonPlanPending || it.PlanStatus != domain.PlanFailed {
		t.Fatalf("item = %s/%q plan %q, want needs_you plan_pending with a failed plan", it.Status, it.Reason, it.PlanStatus)
	}
	if v, _ := e.runs.Get(ctx, "p1", run); v.Status == domain.BacklogRunDone {
		t.Fatal("the run ended while a plan had failed")
	}
	if _, err := e.runs.SetItem(ctx, "p1", orch, run, a, backlog.RunItemUpdate{Status: domain.BacklogRunItemActive}); err == nil {
		t.Fatal("an item whose plan failed was started")
	}
}

func TestRun_ClosedRunKeepsSplitParent(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningAlways)
	a := e.item(t, "A")
	run := e.started(t, a)
	ctx := context.Background()
	if _, err := e.runs.Finish(ctx, "p1", orch, run, "done early"); err != nil {
		t.Fatal(err)
	}
	sub := goodSubmit()
	sub.PRs = []domain.PlanPR{
		{Title: "First", Files: []string{"a.go"}, Tests: "T1", Produces: []string{"One()"}},
		{Title: "Second", Files: []string{"b.go"}, Tests: "T2", Consumes: []string{"One()"}},
	}
	e.approveWith(t, a, sub)
	v, _ := e.runs.Get(ctx, "p1", run)
	if len(v.Items) != 1 || v.Items[0].ItemRef() != a {
		t.Fatalf("a closed run changed its items: %d items", len(v.Items))
	}
}

func TestRun_ConcurrentApprovalsNeverExceedTheCap(t *testing.T) {
	e := newRunPlanEnv(t, domain.PlanningAlways)
	ctx := context.Background()
	var refs []string
	for i := 0; i < domain.MaxBacklogRunWorkers+3; i++ {
		refs = append(refs, e.item(t, "item"))
	}
	e.started(t, refs...)
	ready := refs[:2]
	for _, r := range ready { // both plans submitted, awaiting approval
		pl, _, _ := e.plans.Current(ctx, e.itemID(t, r))
		planner := backlog.Actor{Kind: domain.BacklogOriginWorker, SessionID: string(pl.PlannerSessionID)}
		if _, err := e.plans.SubmitQuestions(ctx, "p1", planner, r, goodQuestions()); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.Defaults(ctx, "p1", human, r, false); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.Submit(ctx, "p1", planner, r, goodSubmit()); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(ready))
	for _, r := range ready {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.plans.Approve(ctx, "p1", human, r, backlog.PlanApproveInput{})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	open, err := e.plans.ListOpen(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(open) > domain.MaxBacklogRunWorkers {
		t.Fatalf("%d open plans, cap is %d", len(open), domain.MaxBacklogRunWorkers)
	}
	if got, want := len(e.sessions.spawned), domain.MaxBacklogRunWorkers+2; got != want {
		t.Fatalf("planners spawned = %d, want %d", got, want)
	}
}
