package backlog_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

type fakeNotifier struct {
	msgs []string
	err  error
}

func (f *fakeNotifier) NotifyOrchestrator(_ context.Context, _ domain.ProjectID, m string) error {
	if f.err != nil {
		return f.err
	}
	f.msgs = append(f.msgs, m)
	return nil
}

type runEnv struct {
	runs   *backlog.RunService
	items  *backlog.Service
	notify *fakeNotifier
	store  *sqlite.Store
}

var (
	human = backlog.Actor{Kind: domain.BacklogOriginHuman}
	orch  = backlog.Actor{Kind: domain.BacklogOriginOrchestrator, SessionID: "o1"}
)

func newRunEnv(t *testing.T, enabled bool) runEnv {
	t.Helper()
	s := sqlitetest.MustOpen(t)
	if err := s.UpsertProject(context.Background(), domain.ProjectRecord{ID: "p1", Path: "/tmp/p1", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	n := &fakeNotifier{}
	return runEnv{
		runs:   backlog.NewRunService(backlog.RunDeps{Store: s, Notify: n, Enabled: enabled}),
		items:  backlog.New(backlog.Deps{Store: s}),
		notify: n, store: s,
	}
}

func (e runEnv) item(t *testing.T, title string) string {
	t.Helper()
	v, err := e.items.Create(context.Background(), "p1", human, backlog.CreateInput{Title: title})
	if err != nil {
		t.Fatal(err)
	}
	return v.Ref()
}

// started creates an orchestrator run with a one-lane plan and returns its ref.
func (e runEnv) started(t *testing.T, refs ...string) string {
	t.Helper()
	ctx := context.Background()
	run, err := e.runs.Start(ctx, "p1", orch, backlog.RunStartInput{Scope: domain.BacklogScopePR, Kind: domain.BacklogRunSelectAll})
	if err != nil {
		t.Fatal(err)
	}
	var lane []backlog.RunPlanItem
	for _, r := range refs {
		lane = append(lane, backlog.RunPlanItem{Ref: r, Model: "sonnet"})
	}
	if _, err := e.runs.SavePlan(ctx, "p1", orch, run.Ref(), backlog.RunPlanInput{Lanes: [][]backlog.RunPlanItem{lane}}); err != nil {
		t.Fatal(err)
	}
	return run.Ref()
}

// startedLanes is started with one lane per ref list, so items run in parallel.
func (e runEnv) startedLanes(t *testing.T, lanes ...[]string) string {
	t.Helper()
	ctx := context.Background()
	run, err := e.runs.Start(ctx, "p1", orch, backlog.RunStartInput{Scope: domain.BacklogScopePR, Kind: domain.BacklogRunSelectAll})
	if err != nil {
		t.Fatal(err)
	}
	var plan backlog.RunPlanInput
	for _, refs := range lanes {
		var lane []backlog.RunPlanItem
		for _, r := range refs {
			lane = append(lane, backlog.RunPlanItem{Ref: r, Model: "sonnet"})
		}
		plan.Lanes = append(plan.Lanes, lane)
	}
	if _, err := e.runs.SavePlan(ctx, "p1", orch, run.Ref(), plan); err != nil {
		t.Fatal(err)
	}
	return run.Ref()
}

func (e runEnv) set(run, item string, st domain.BacklogRunItemStatus) error {
	_, err := e.runs.SetItem(context.Background(), "p1", orch, run, item, backlog.RunItemUpdate{Status: st, Reason: "r", Question: "q?"})
	return err
}

func TestRunRequestPostsExactlyOneMessageForHumanOnly(t *testing.T) {
	e := newRunEnv(t, true)
	ctx := context.Background()
	in := backlog.RunStartInput{Scope: domain.BacklogScopePR, Kind: domain.BacklogRunSelectAll}
	if _, err := e.runs.Start(ctx, "p1", human, in); err != nil {
		t.Fatal(err)
	}
	if len(e.notify.msgs) != 1 {
		t.Fatalf("human start sent %d messages, want exactly 1: %q", len(e.notify.msgs), e.notify.msgs)
	}
	if _, err := e.runs.Stop(ctx, "p1", orch, "R1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.runs.Start(ctx, "p1", orch, in); err != nil {
		t.Fatal(err)
	}
	if len(e.notify.msgs) != 1 {
		t.Fatalf("orchestrator start sent a message: %q", e.notify.msgs)
	}
}

func TestRunRequestWithoutOrchestratorCancelsTheRun(t *testing.T) {
	e := newRunEnv(t, true)
	ctx := context.Background()
	in := backlog.RunStartInput{Scope: domain.BacklogScopePR, Kind: domain.BacklogRunSelectAll}
	e.notify.err = backlog.ErrNoOrchestrator
	_, err := e.runs.Start(ctx, "p1", human, in)
	if apiCode(t, err) != "BACKLOG_RUN_NO_ORCHESTRATOR" {
		t.Fatalf("err = %v", err)
	}
	if len(e.notify.msgs) != 0 {
		t.Fatalf("messages = %q, want none", e.notify.msgs)
	}
	open, err := e.runs.List(ctx, "p1", true, 0)
	if err != nil || len(open) != 0 {
		t.Fatalf("open runs after undeliverable request = %+v, %v; want none (slot freed)", open, err)
	}
	e.notify.err = nil
	if _, err := e.runs.Start(ctx, "p1", human, in); err != nil {
		t.Fatalf("retry after cancel: %v", err)
	}
}

func TestRunModeOffRefusesEverything(t *testing.T) {
	e := newRunEnv(t, false)
	ctx := context.Background()
	calls := map[string]func() error{
		"start": func() error {
			_, err := e.runs.Start(ctx, "p1", human, backlog.RunStartInput{Scope: domain.BacklogScopePR, Kind: domain.BacklogRunSelectAll})
			return err
		},
		"get":    func() error { _, err := e.runs.Get(ctx, "p1", "R1"); return err },
		"list":   func() error { _, err := e.runs.List(ctx, "p1", false, 0); return err },
		"plan":   func() error { _, err := e.runs.SavePlan(ctx, "p1", orch, "R1", backlog.RunPlanInput{}); return err },
		"pause":  func() error { _, err := e.runs.Pause(ctx, "p1", human, "R1", backlog.RunPauseInput{}); return err },
		"resume": func() error { _, err := e.runs.Resume(ctx, "p1", human, "R1"); return err },
		"stop":   func() error { _, err := e.runs.Stop(ctx, "p1", human, "R1"); return err },
		"item": func() error {
			_, err := e.runs.SetItem(ctx, "p1", orch, "R1", "B1", backlog.RunItemUpdate{})
			return err
		},
		"finish": func() error { _, err := e.runs.Finish(ctx, "p1", orch, "R1", "x"); return err },
		"usage":  func() error { _, err := e.runs.ItemUsage(ctx, "p1", "B1"); return err },
	}
	for name, call := range calls {
		if code := apiCode(t, call()); code != "BACKLOG_RUN_DISABLED" {
			t.Errorf("%s: code = %s, want BACKLOG_RUN_DISABLED", name, code)
		}
	}
	if len(e.notify.msgs) != 0 {
		t.Fatalf("switch off still sent %q", e.notify.msgs)
	}
}

func TestOnlyOwnerAndOrchestratorUseRuns(t *testing.T) {
	e := newRunEnv(t, true)
	ctx := context.Background()
	worker := backlog.Actor{Kind: domain.BacklogOriginWorker, SessionID: "w1"}
	if _, err := e.runs.Start(ctx, "p1", worker, backlog.RunStartInput{Scope: domain.BacklogScopePR, Kind: domain.BacklogRunSelectAll}); apiCode(t, err) != "BACKLOG_RUN_FORBIDDEN" {
		t.Fatalf("worker start err = %v", err)
	}
	ref := e.started(t, e.item(t, "a"))
	if _, err := e.runs.SetItem(ctx, "p1", human, ref, "B1", backlog.RunItemUpdate{Status: domain.BacklogRunItemActive}); apiCode(t, err) != "BACKLOG_RUN_FORBIDDEN" {
		t.Fatalf("human set item err = %v (only the orchestrator writes progress)", err)
	}
}

func TestRunPlanValidation(t *testing.T) {
	e := newRunEnv(t, true)
	ctx := context.Background()
	a, b, c := e.item(t, "a"), e.item(t, "b"), e.item(t, "c")
	if _, err := e.items.Link(ctx, "p1", b, domain.BacklogLinkBlockedBy, a, "human"); err != nil {
		t.Fatal(err)
	}
	parent, err := e.items.Create(ctx, "p1", human, backlog.CreateInput{Title: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.items.Create(ctx, "p1", human, backlog.CreateInput{Title: "child", ParentRef: parent.Ref()}); err != nil {
		t.Fatal(err)
	}
	run, err := e.runs.Start(ctx, "p1", orch, backlog.RunStartInput{Scope: domain.BacklogScopePR, Kind: domain.BacklogRunSelectAll})
	if err != nil {
		t.Fatal(err)
	}
	it := func(ref string) backlog.RunPlanItem { return backlog.RunPlanItem{Ref: ref, Model: "opus"} }
	cases := map[string]backlog.RunPlanInput{
		"empty plan":                   {},
		"duplicate":                    {Lanes: [][]backlog.RunPlanItem{{it(a), it(a)}}},
		"duplicate across lanes":       {Lanes: [][]backlog.RunPlanItem{{it(a)}, {it(a)}}},
		"parent listed":                {Lanes: [][]backlog.RunPlanItem{{it(parent.Ref())}}},
		"missing model":                {Lanes: [][]backlog.RunPlanItem{{{Ref: a}}}},
		"blocker after blocked":        {Lanes: [][]backlog.RunPlanItem{{it(b), it(a)}}},
		"blocker in another lane":      {Lanes: [][]backlog.RunPlanItem{{it(a)}, {it(b)}}},
		"blocker open and not planned": {Lanes: [][]backlog.RunPlanItem{{it(b), it(c)}}},
	}
	for name, plan := range cases {
		if _, err := e.runs.SavePlan(ctx, "p1", orch, run.Ref(), plan); err == nil {
			t.Errorf("%s: plan accepted", name)
		} else if code := apiCode(t, err); code != "INVALID_BACKLOG_RUN" {
			t.Errorf("%s: code = %s, want INVALID_BACKLOG_RUN", name, code)
		}
	}
	if _, err := e.runs.SavePlan(ctx, "p1", orch, run.Ref(), backlog.RunPlanInput{Lanes: [][]backlog.RunPlanItem{{it("B99")}}}); apiCode(t, err) != "BACKLOG_ITEM_NOT_FOUND" {
		t.Errorf("unknown ref: %v", err)
	}
	ok := backlog.RunPlanInput{Lanes: [][]backlog.RunPlanItem{{it(a), it(b)}, {it(c)}}}
	v, err := e.runs.SavePlan(ctx, "p1", orch, run.Ref(), ok)
	if err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	if v.Status != domain.BacklogRunRunning || v.StartedAt == nil || len(v.Items) != 3 {
		t.Fatalf("after plan: status %s items %d", v.Status, len(v.Items))
	}
}

func TestRunSingleOpenRunPerProject(t *testing.T) {
	e := newRunEnv(t, true)
	e.started(t, e.item(t, "a"))
	_, err := e.runs.Start(context.Background(), "p1", orch, backlog.RunStartInput{Scope: domain.BacklogScopePR, Kind: domain.BacklogRunSelectAll})
	if apiCode(t, err) != "BACKLOG_RUN_OPEN" {
		t.Fatalf("second open run err = %v", err)
	}
}

func TestRunWorkerCapIsThree(t *testing.T) {
	e := newRunEnv(t, true)
	refs := []string{e.item(t, "a"), e.item(t, "b"), e.item(t, "c"), e.item(t, "d")}
	run := e.startedLanes(t, refs[:1], refs[1:2], refs[2:3], refs[3:])
	for _, r := range refs[:3] {
		if err := e.set(run, r, domain.BacklogRunItemActive); err != nil {
			t.Fatal(err)
		}
	}
	if code := apiCode(t, e.set(run, refs[3], domain.BacklogRunItemActive)); code != "BACKLOG_RUN_WORKER_CAP" {
		t.Fatalf("4th active code = %s", code)
	}
	// In review frees the slot: the worker is done, the PR waits on a review.
	if err := e.set(run, refs[0], domain.BacklogRunItemInReview); err != nil {
		t.Fatal(err)
	}
	if err := e.set(run, refs[3], domain.BacklogRunItemActive); err != nil {
		t.Fatalf("4th active after one went in_review: %v", err)
	}
}

func TestRunSlotFreesWhenTheItemClosedItself(t *testing.T) {
	e := newRunEnv(t, true)
	refs := []string{e.item(t, "a"), e.item(t, "b"), e.item(t, "c"), e.item(t, "d")}
	run := e.startedLanes(t, refs[:1], refs[1:2], refs[2:3], refs[3:])
	for _, r := range refs[:3] {
		_ = e.set(run, r, domain.BacklogRunItemActive)
	}
	// The PR merged and lifecycle closed the item; the stored status is still active.
	if _, err := e.items.SetState(context.Background(), "p1", refs[0], domain.BacklogDone, "merged"); err != nil {
		t.Fatal(err)
	}
	if err := e.set(run, refs[3], domain.BacklogRunItemActive); err != nil {
		t.Fatalf("a merged item still holds a slot: %v", err)
	}
}

func TestRunPlanCannotDropItemsWithWorkOnRecord(t *testing.T) {
	e := newRunEnv(t, true)
	ctx := context.Background()
	b1, b2, b3, b4, b5 := e.item(t, "1"), e.item(t, "2"), e.item(t, "3"), e.item(t, "4"), e.item(t, "5")
	run := e.startedLanes(t, []string{b1}, []string{b2}, []string{b3}, []string{b4}, []string{b5})
	for _, r := range []string{b1, b2, b3} {
		_ = e.set(run, r, domain.BacklogRunItemActive)
	}
	replan := func(refs ...string) error {
		var plan backlog.RunPlanInput
		for _, r := range refs {
			plan.Lanes = append(plan.Lanes, []backlog.RunPlanItem{{Ref: r, Model: "sonnet"}})
		}
		_, err := e.runs.SavePlan(ctx, "p1", orch, run, plan)
		return err
	}
	// B1 still has a worker: dropping it would hide it from the cap.
	err := replan(b2, b3, b4, b5)
	if apiCode(t, err) != "INVALID_BACKLOG_RUN" || !strings.Contains(err.Error(), "B1 is active; it must stay in the plan") {
		t.Fatalf("dropping an active item: %v", err)
	}
	if code := apiCode(t, e.set(run, b4, domain.BacklogRunItemActive)); code != "BACKLOG_RUN_WORKER_CAP" {
		t.Fatalf("cap after the refused replan = %s, want BACKLOG_RUN_WORKER_CAP", code)
	}
	v, _ := e.runs.Get(ctx, "p1", run)
	if len(v.Items) != 5 {
		t.Fatalf("refused replan changed the plan: %d items", len(v.Items))
	}
	// A once-failed item keeps its attempts: it cannot be dropped and re-added.
	_ = e.set(run, b3, domain.BacklogRunItemFailed)
	if err := replan(b1, b2, b4, b5); apiCode(t, err) != "INVALID_BACKLOG_RUN" {
		t.Fatalf("dropping a once-failed item: %v", err)
	}
	// A landed item stays too; a queued one may go.
	_ = e.set(run, b2, domain.BacklogRunItemLanded)
	if err := replan(b1, b3, b4, b5); apiCode(t, err) != "INVALID_BACKLOG_RUN" {
		t.Fatalf("dropping a landed item: %v", err)
	}
	if err := replan(b1, b2, b3, b4); err != nil {
		t.Fatalf("dropping a queued item: %v", err)
	}
}

func TestRunItemWaitsForTheItemAheadInItsLane(t *testing.T) {
	e := newRunEnv(t, true)
	a, b, c := e.item(t, "a"), e.item(t, "b"), e.item(t, "c")
	run := e.startedLanes(t, []string{a, b}, []string{c})
	if code := apiCode(t, e.set(run, b, domain.BacklogRunItemActive)); code != "BACKLOG_RUN_TRANSITION" {
		t.Fatalf("starting B before the item ahead of it in its lane: %s", code)
	}
	if err := e.set(run, c, domain.BacklogRunItemActive); err != nil {
		t.Fatalf("an independent lane is not held up: %v", err)
	}
	_ = e.set(run, a, domain.BacklogRunItemLanded)
	if err := e.set(run, b, domain.BacklogRunItemActive); err != nil {
		t.Fatalf("starting B after A landed: %v", err)
	}
}

func TestRunStartRefusedWhileAnUnplannedBlockerIsOpen(t *testing.T) {
	e := newRunEnv(t, true)
	a, blocker := e.item(t, "a"), e.item(t, "blocker")
	if _, err := e.items.Link(context.Background(), "p1", a, domain.BacklogLinkBlockedBy, blocker, "human"); err != nil {
		t.Fatal(err)
	}
	// A plan cannot hold a blocked item whose blocker is open and unplanned; closing it first lets the plan in,
	// reopening it afterwards must still hold the item back.
	if _, err := e.items.SetState(context.Background(), "p1", blocker, domain.BacklogDone, "x"); err != nil {
		t.Fatal(err)
	}
	run := e.started(t, a, e.item(t, "other"))
	if _, err := e.items.SetState(context.Background(), "p1", blocker, domain.BacklogOpen, ""); err != nil {
		t.Fatal(err)
	}
	if code := apiCode(t, e.set(run, a, domain.BacklogRunItemActive)); code != "BACKLOG_RUN_TRANSITION" {
		t.Fatalf("blocked start code = %s", code)
	}
}

func TestRunTotalElapsedIsTheRunsOwnSpan(t *testing.T) {
	s := sqlitetest.MustOpen(t)
	ctx := context.Background()
	if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "p1", Path: "/tmp/p1", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	runs := backlog.NewRunService(backlog.RunDeps{Store: s, Enabled: true, Now: func() time.Time { return now }})
	items := backlog.New(backlog.Deps{Store: s})
	a, _ := items.Create(ctx, "p1", human, backlog.CreateInput{Title: "a"})
	b, _ := items.Create(ctx, "p1", human, backlog.CreateInput{Title: "b"})
	run, err := runs.Start(ctx, "p1", orch, backlog.RunStartInput{Scope: domain.BacklogScopePR, Kind: domain.BacklogRunSelectAll})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour) // the plan, and so the run's start, comes an hour after the request
	plan := backlog.RunPlanInput{Lanes: [][]backlog.RunPlanItem{{{Ref: a.Ref(), Model: "m"}}, {{Ref: b.Ref(), Model: "m"}}}}
	if _, err := runs.SavePlan(ctx, "p1", orch, run.Ref(), plan); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	v, err := runs.Get(ctx, "p1", run.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if v.Usage.ElapsedSeconds == nil || *v.Usage.ElapsedSeconds != 7200 {
		t.Fatalf("run elapsed = %v, want 7200s from the run's own start", v.Usage.ElapsedSeconds)
	}
}

func TestRunSecondFailureHoldsTheItem(t *testing.T) {
	e := newRunEnv(t, true)
	a := e.item(t, "a")
	run := e.started(t, a, e.item(t, "b"))
	_ = e.set(run, a, domain.BacklogRunItemActive)
	_ = e.set(run, a, domain.BacklogRunItemFailed)
	v, _ := e.runs.Get(context.Background(), "p1", run)
	if got := v.Items[0]; got.Status != domain.BacklogRunItemFailed || got.Attempts != 1 {
		t.Fatalf("after first failure: %s attempts %d, want failed/1", got.Status, got.Attempts)
	}
	_ = e.set(run, a, domain.BacklogRunItemActive)
	_ = e.set(run, a, domain.BacklogRunItemFailed)
	v, _ = e.runs.Get(context.Background(), "p1", run)
	if got := v.Items[0]; got.Status != domain.BacklogRunItemHeld || got.Reason == "" {
		t.Fatalf("after second failure: %s reason %q, want held with a reason", got.Status, got.Reason)
	}
}

func TestRunItemRules(t *testing.T) {
	e := newRunEnv(t, true)
	a := e.item(t, "a")
	run := e.started(t, a, e.item(t, "b")) // a second item keeps the run open after a lands
	ctx := context.Background()
	if _, err := e.runs.SetItem(ctx, "p1", orch, run, a, backlog.RunItemUpdate{Status: domain.BacklogRunItemNeedsYou}); apiCode(t, err) != "INVALID_BACKLOG_RUN" {
		t.Fatalf("needs_you without a question: %v", err)
	}
	if _, err := e.runs.SetItem(ctx, "p1", orch, run, a, backlog.RunItemUpdate{Status: domain.BacklogRunItemHeld}); apiCode(t, err) != "INVALID_BACKLOG_RUN" {
		t.Fatalf("held without a reason: %v", err)
	}
	if err := e.set(run, a, domain.BacklogRunItemLanded); err != nil {
		t.Fatal(err)
	}
	if err := e.set(run, a, domain.BacklogRunItemQueued); apiCode(t, err) != "BACKLOG_RUN_TRANSITION" {
		t.Fatalf("leaving landed: %v", err)
	}
}

func TestRunEndsWhenEveryItemSettles(t *testing.T) {
	e := newRunEnv(t, true)
	a, b := e.item(t, "a"), e.item(t, "b")
	run := e.started(t, a, b)
	ctx := context.Background()
	_ = e.set(run, a, domain.BacklogRunItemLanded)
	if v, _ := e.runs.Get(ctx, "p1", run); v.Status != domain.BacklogRunRunning {
		t.Fatalf("status = %s with b unsettled, want running", v.Status)
	}
	_ = e.set(run, b, domain.BacklogRunItemNeedsYou) // needs_you settles: the run carries on without it
	v, _ := e.runs.Get(ctx, "p1", run)
	if v.Status != domain.BacklogRunDone || v.EndedAt == nil {
		t.Fatalf("status = %s ended %v, want done", v.Status, v.EndedAt)
	}
	if err := e.set(run, a, domain.BacklogRunItemLanded); apiCode(t, err) != "BACKLOG_RUN_TRANSITION" {
		t.Fatalf("write to a done run: %v", err)
	}
}

func TestRunStopFinishesInFlightAndStartsNothing(t *testing.T) {
	e := newRunEnv(t, true)
	a, b := e.item(t, "a"), e.item(t, "b")
	run := e.started(t, a, b)
	ctx := context.Background()
	_ = e.set(run, a, domain.BacklogRunItemActive)
	v, err := e.runs.Stop(ctx, "p1", human, run)
	if err != nil || v.Status != domain.BacklogRunStopping {
		t.Fatalf("stop with a in flight: %v status %s, want stopping", err, v.Status)
	}
	if code := apiCode(t, e.set(run, b, domain.BacklogRunItemActive)); code != "BACKLOG_RUN_TRANSITION" {
		t.Fatalf("start during stopping: %s", code)
	}
	if _, err := e.runs.Stop(ctx, "p1", human, run); apiCode(t, err) != "BACKLOG_RUN_TRANSITION" {
		t.Fatalf("second stop: %v", err)
	}
	_ = e.set(run, a, domain.BacklogRunItemLanded)
	if v, _ := e.runs.Get(ctx, "p1", run); v.Status != domain.BacklogRunDone {
		t.Fatalf("status = %s after the last in-flight item landed, want done", v.Status)
	}
}

func TestRunPauseResumeAndHumanNudges(t *testing.T) {
	e := newRunEnv(t, true)
	run := e.started(t, e.item(t, "a"))
	ctx := context.Background()
	if v, err := e.runs.Pause(ctx, "p1", human, run, backlog.RunPauseInput{Reason: "usage limit"}); err != nil || v.Status != domain.BacklogRunPaused {
		t.Fatalf("pause: %v %s", err, v.Status)
	}
	if code := apiCode(t, e.set(run, "B1", domain.BacklogRunItemActive)); code != "BACKLOG_RUN_TRANSITION" {
		t.Fatalf("start while paused: %s", code)
	}
	if v, err := e.runs.Resume(ctx, "p1", human, run); err != nil || v.Status != domain.BacklogRunRunning || v.PauseReason != "" {
		t.Fatalf("resume: %v %+v", err, v.BacklogRun)
	}
	if _, err := e.runs.Resume(ctx, "p1", human, run); apiCode(t, err) != "BACKLOG_RUN_TRANSITION" {
		t.Fatalf("resume a running run: %v", err)
	}
	if len(e.notify.msgs) != 2 {
		t.Fatalf("nudges = %d, want one per successful human pause/resume (2): %q", len(e.notify.msgs), e.notify.msgs)
	}
	// An orchestrator-driven pause sends nothing.
	if _, err := e.runs.Pause(ctx, "p1", orch, run, backlog.RunPauseInput{}); err != nil {
		t.Fatal(err)
	}
	if len(e.notify.msgs) != 2 {
		t.Fatalf("orchestrator pause nudged: %q", e.notify.msgs)
	}
}

func TestRunFinishNeedsNothingInFlight(t *testing.T) {
	e := newRunEnv(t, true)
	a := e.item(t, "a")
	run := e.started(t, a, e.item(t, "b"))
	ctx := context.Background()
	_ = e.set(run, a, domain.BacklogRunItemActive)
	if _, err := e.runs.Finish(ctx, "p1", orch, run, "done"); apiCode(t, err) != "BACKLOG_RUN_TRANSITION" {
		t.Fatalf("finish with a in flight: %v", err)
	}
	_ = e.set(run, a, domain.BacklogRunItemLanded)
	v, err := e.runs.Finish(ctx, "p1", orch, run, "A landed, B left queued")
	if err != nil || v.Status != domain.BacklogRunDone || v.Summary != "A landed, B left queued" {
		t.Fatalf("finish: %v %+v", err, v.BacklogRun)
	}
}

func TestRunDisplayStatusFollowsTheItem(t *testing.T) {
	e := newRunEnv(t, true)
	a, b, c := e.item(t, "a"), e.item(t, "b"), e.item(t, "c")
	run := e.started(t, a, b, c)
	ctx := context.Background()
	_ = e.set(run, a, domain.BacklogRunItemActive)
	if _, err := e.items.SetState(ctx, "p1", a, domain.BacklogDone, "merged"); err != nil { // lifecycle closed it
		t.Fatal(err)
	}
	hold := "waiting"
	if _, err := e.items.Update(ctx, "p1", b, backlog.UpdateInput{Hold: &hold}); err != nil {
		t.Fatal(err)
	}
	v, _ := e.runs.Get(ctx, "p1", run)
	got := map[string]domain.BacklogRunItemStatus{}
	for _, it := range v.Items {
		got[it.ItemRef()] = it.DisplayStatus
	}
	if got[a] != domain.BacklogRunItemLanded || got[b] != domain.BacklogRunItemHeld || got[c] != domain.BacklogRunItemQueued {
		t.Fatalf("display statuses = %v, want a landed, b held, c queued (stored a is still active)", got)
	}
	if v.Items[0].Status != domain.BacklogRunItemActive {
		t.Fatal("display override must not rewrite the stored status")
	}
}

type usageStore struct {
	*sqlite.Store
	usage store.BacklogItemUsage
}

func (u usageStore) BacklogItemUsageFor(context.Context, string) (store.BacklogItemUsage, error) {
	return u.usage, nil
}

func TestItemUsageCostAndElapsed(t *testing.T) {
	s := sqlitetest.MustOpen(t)
	ctx := context.Background()
	if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "p1", Path: "/tmp/p1", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	now := start.Add(90 * time.Minute)
	mk := func(u store.BacklogItemUsage) *backlog.RunService {
		return backlog.NewRunService(backlog.RunDeps{Store: usageStore{s, u}, Enabled: true, Now: func() time.Time { return now }})
	}
	items := backlog.New(backlog.Deps{Store: s})
	if _, err := items.Create(ctx, "p1", human, backlog.CreateInput{Title: "a"}); err != nil {
		t.Fatal(err)
	}
	got, err := mk(store.BacklogItemUsage{PricedNanos: 4000, Events: 3, PricedEvents: 2, FirstStarted: start}).ItemUsage(ctx, "p1", "B1")
	if err != nil {
		t.Fatal(err)
	}
	if got.CostNanos == nil || *got.CostNanos != 4000 || got.CostComplete {
		t.Fatalf("cost = %v complete %v, want 4000 and incomplete (an unpriced event)", got.CostNanos, got.CostComplete)
	}
	if got.ElapsedSeconds == nil || *got.ElapsedSeconds != 5400 {
		t.Fatalf("elapsed = %v, want 5400s wall-clock from the first session start", got.ElapsedSeconds)
	}
	none, _ := mk(store.BacklogItemUsage{}).ItemUsage(ctx, "p1", "B1")
	if none.CostNanos != nil || none.ElapsedSeconds != nil {
		t.Fatalf("usage with no sessions = %+v, want nothing", none)
	}
}

// stopBeforeSave simulates an owner Stop landing between SavePlan's read of the
// run and its transaction.
type stopBeforeSave struct{ *sqlite.Store }

func (s stopBeforeSave) SaveBacklogRunPlan(ctx context.Context, runID string, plan []store.BacklogRunPlanItem, now time.Time, mutate func(*domain.BacklogRun) error) (domain.BacklogRun, error) {
	if _, _, err := s.UpdateBacklogRunState(ctx, runID, now, func(r *domain.BacklogRun, _ []domain.BacklogRunItem, _ int) error {
		r.Status = domain.BacklogRunStopping
		return nil
	}); err != nil {
		return domain.BacklogRun{}, err
	}
	return s.Store.SaveBacklogRunPlan(ctx, runID, plan, now, mutate)
}

func TestRunPlanRefusedWhenStopLandsMidSave(t *testing.T) {
	s := sqlitetest.MustOpen(t)
	ctx := context.Background()
	if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "p1", Path: "/tmp/p1", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	runs := backlog.NewRunService(backlog.RunDeps{Store: stopBeforeSave{s}, Enabled: true})
	a, _ := backlog.New(backlog.Deps{Store: s}).Create(ctx, "p1", human, backlog.CreateInput{Title: "a"})
	run, err := runs.Start(ctx, "p1", orch, backlog.RunStartInput{Scope: domain.BacklogScopePR, Kind: domain.BacklogRunSelectAll})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runs.SavePlan(ctx, "p1", orch, run.Ref(), backlog.RunPlanInput{Lanes: [][]backlog.RunPlanItem{{{Ref: a.Ref(), Model: "m"}}}})
	if apiCode(t, err) != "BACKLOG_RUN_TRANSITION" {
		t.Fatalf("plan saved onto a stopping run: %v", err)
	}
	got, _ := runs.Get(ctx, "p1", run.Ref())
	if len(got.Items) != 0 {
		t.Fatalf("refused plan still wrote %d items", len(got.Items))
	}
}

func TestPrepareSpawnEmptyScopeFollowsOpenRun(t *testing.T) {
	e := newRunEnv(t, true)
	ctx := context.Background()
	ref := e.item(t, "no own scope")
	if _, err := e.runs.Start(ctx, "p1", orch, backlog.RunStartInput{Scope: domain.BacklogScopePR, Kind: domain.BacklogRunSelectAll}); err != nil {
		t.Fatal(err)
	}
	got, err := e.items.PrepareSpawn(ctx, "p1", ref, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Context, "Push your branch and open a PR") || strings.Contains(got.Context, "Do not push") || got.Warning != "" {
		t.Fatalf("context = %q warning = %q", got.Context, got.Warning)
	}
}

func TestPrepareSpawnEmptyScopeIgnoresRunThatDoesNotCoverItem(t *testing.T) {
	e := newRunEnv(t, true)
	ctx := context.Background()
	parent := e.item(t, "parent")
	if _, err := e.items.Create(ctx, "p1", human, backlog.CreateInput{Title: "child", ParentRef: parent}); err != nil {
		t.Fatal(err)
	}
	outside := e.item(t, "outside")
	if _, err := e.runs.Start(ctx, "p1", orch, backlog.RunStartInput{Scope: domain.BacklogScopePRMerge, Kind: domain.BacklogRunSelectParent, Refs: []string{parent}}); err != nil {
		t.Fatal(err)
	}
	got, err := e.items.PrepareSpawn(ctx, "p1", outside, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Context, "merge it") || !strings.Contains(got.Context, "no run scope applies") || got.Warning == "" {
		t.Fatalf("context = %q warning = %q", got.Context, got.Warning)
	}
}

func TestPrepareSpawnEmptyScopeCoversDescendantsOfSelectedRef(t *testing.T) {
	e := newRunEnv(t, true)
	ctx := context.Background()
	parent := e.item(t, "parent")
	child, err := e.items.Create(ctx, "p1", human, backlog.CreateInput{Title: "child", ParentRef: parent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.runs.Start(ctx, "p1", orch, backlog.RunStartInput{Scope: domain.BacklogScopePR, Kind: domain.BacklogRunSelectRefs, Refs: []string{parent}}); err != nil {
		t.Fatal(err)
	}
	got, err := e.items.PrepareSpawn(ctx, "p1", child.Ref(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Context, "Push your branch and open a PR") || got.Warning != "" {
		t.Fatalf("context = %q warning = %q", got.Context, got.Warning)
	}
}
