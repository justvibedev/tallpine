package backlog_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
)

// SpawnInPlace starts an ordinary worker session (no planner role), as the
// session manager would for an in-place implementer.
func (f *fakeSessions) SpawnInPlace(ctx context.Context, in backlog.PlannerSpawn) (domain.SessionID, error) {
	return f.newWorker(ctx, in.ProjectID, &in)
}

func (f *fakeSessions) newWorker(ctx context.Context, project domain.ProjectID, in *backlog.PlannerSpawn) (domain.SessionID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	rec, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: project, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		Metadata: domain.SessionMetadata{Branch: "b/x", WorkspacePath: "/ws"}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return "", err
	}
	if in != nil {
		f.inPlace = append(f.inPlace, *in)
	}
	return rec.ID, nil
}

// inPlaceEnv is a project that plans before it builds with the switch on.
func inPlaceEnv(t *testing.T) *planEnv {
	t.Helper()
	return newPlanEnvWith(t, true, func(d *backlog.PlanDeps) {
		d.Mode = func(context.Context, domain.ProjectID) domain.PlanningMode { return domain.PlanningAlways }
		d.InPlace = func(context.Context, domain.ProjectID) bool { return true }
	})
}

// startInPlace does what the spawn controller does: prepare (reserve), spawn a
// worker, attach it as the author. It returns the plan and the author.
func (e *planEnv) startInPlace() (domain.BacklogPlan, domain.SessionID) {
	e.t.Helper()
	sp, err := e.bl.PrepareSpawnPlan(e.ctx, "p1", e.item.Ref(), false, false, "")
	if err != nil || sp.PlanID == "" {
		e.t.Fatalf("PrepareSpawnPlan = %+v, %v", sp, err)
	}
	sid, err := e.sessions.newWorker(e.ctx, "p1", nil)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.bl.AttachInPlaceAuthor(e.ctx, sp.PlanID, sid); err != nil {
		e.t.Fatal(err)
	}
	if err := e.bl.LinkSession(e.ctx, e.item.ID, sid, "orch"); err != nil {
		e.t.Fatal(err)
	}
	plan, err := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if err != nil {
		e.t.Fatal(err)
	}
	return plan, sid
}

func (e *planEnv) author(sid domain.SessionID) backlog.Actor {
	return backlog.Actor{Kind: domain.BacklogOriginWorker, SessionID: string(sid)}
}

func TestInPlaceSpawnReservesDraftingPlanWithImplementerAuthor(t *testing.T) {
	e := inPlaceEnv(t)
	sp, err := e.bl.PrepareSpawnPlan(e.ctx, "p1", e.item.Ref(), false, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if sp.PlanID == "" || !strings.Contains(sp.Context, "Plan in place") || !strings.Contains(sp.Context, domain.PlanFormatPrompt[:40]) {
		t.Fatalf("spawn item = %+v", sp)
	}
	plan, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if plan.ID != sp.PlanID || plan.Status != domain.PlanDrafting || plan.PlannerSessionID != "" {
		t.Fatalf("reserved plan = %+v", plan)
	}
	plan, sid := e.startInPlace2(sp.PlanID)
	if plan.PlannerSessionID != sid || len(e.sessions.spawned) != 0 {
		t.Fatalf("author %q planner spawns %d", plan.PlannerSessionID, len(e.sessions.spawned))
	}
	rec, _, _ := e.store.GetSession(e.ctx, sid)
	if rec.Role == domain.SessionRolePlanner {
		t.Fatal("the author must be an ordinary worker, not a planner")
	}
}

func (e *planEnv) startInPlace2(planID string) (domain.BacklogPlan, domain.SessionID) {
	e.t.Helper()
	sid, _ := e.sessions.newWorker(e.ctx, "p1", nil)
	if err := e.bl.AttachInPlaceAuthor(e.ctx, planID, sid); err != nil {
		e.t.Fatal(err)
	}
	p, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	return p, sid
}

func TestInPlaceArmSelection(t *testing.T) {
	cases := []struct {
		name   string
		on     bool
		arm    string
		code   string // "" = in-place plan reserved
		reject bool
	}{
		{"switch on, no flag", true, "", "", false},
		{"switch on, separate", true, "separate", "PLAN_REQUIRED", true},
		{"switch off, no flag", false, "", "PLAN_REQUIRED", true},
		{"switch off, in-place", false, "in-place", "PLAN_IN_PLACE_OFF", true},
		{"switch off, junk", true, "junk", "PLAN_ARM_INVALID", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newPlanEnvWith(t, true, func(d *backlog.PlanDeps) {
				d.Mode = func(context.Context, domain.ProjectID) domain.PlanningMode { return domain.PlanningAlways }
				d.InPlace = func(context.Context, domain.ProjectID) bool { return tc.on }
			})
			sp, err := e.bl.PrepareSpawnPlan(e.ctx, "p1", e.item.Ref(), false, false, tc.arm)
			if tc.reject {
				if err == nil || apiCode(t, err) != tc.code {
					t.Fatalf("err = %v, want %s", err, tc.code)
				}
				return
			}
			if err != nil || sp.PlanID == "" {
				t.Fatalf("got %+v, %v", sp, err)
			}
		})
	}
}

func TestInPlaceRefusesWhenAPlanIsOpen(t *testing.T) {
	e := inPlaceEnv(t)
	e.startInPlace()
	_, err := e.bl.PrepareSpawnPlan(e.ctx, "p1", e.item.Ref(), false, true, "")
	if err == nil || apiCode(t, err) != "PLAN_OPEN" {
		t.Fatalf("err = %v, want PLAN_OPEN", err)
	}
}

func TestInPlaceOnlyTheAuthorSubmits(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	other, _ := e.sessions.newWorker(e.ctx, "p1", nil)
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.author(other), e.item.Ref(), goodQuestions()); err == nil || apiCode(t, err) != "PLAN_NOT_PLANNER" {
		t.Fatalf("another worker: err = %v", err)
	}
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", planOrch, e.item.Ref(), goodQuestions()); err == nil {
		t.Fatal("the orchestrator must not submit questions")
	}
	p, err := e.plans.SubmitQuestions(e.ctx, "p1", e.author(sid), e.item.Ref(), goodQuestions())
	if err != nil || p.Status != domain.PlanAwaitingAnswers {
		t.Fatalf("author questions = %v, %v", p.Status, err)
	}
}

func TestInPlaceQuestionsAndSubmitRefusedOnceTheAuthorHasAPR(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	pr := domain.PullRequest{URL: "https://github.com/o/r/pull/1", SessionID: sid, Number: 1, UpdatedAt: time.Now().UTC()}
	if err := e.store.WritePR(e.ctx, pr, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.author(sid), e.item.Ref(), goodQuestions()); err == nil || apiCode(t, err) != "PLAN_AUTHOR_HAS_PR" {
		t.Fatalf("questions err = %v", err)
	}
	if _, err := e.plans.Submit(e.ctx, "p1", e.author(sid), e.item.Ref(), goodSubmit()); err == nil || apiCode(t, err) != "PLAN_AUTHOR_HAS_PR" {
		t.Fatalf("submit err = %v", err)
	}
}

func TestInPlaceOpenPlanOfGatesReview(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	if ref, open := e.plans.OpenPlanOf(e.ctx, sid); !open || ref != e.item.Ref() {
		t.Fatalf("drafting: %q %v", ref, open)
	}
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.author(sid), e.item.Ref(), backlog.PlanQuestionsInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.Submit(e.ctx, "p1", e.author(sid), e.item.Ref(), goodSubmit()); err != nil {
		t.Fatal(err)
	}
	if _, open := e.plans.OpenPlanOf(e.ctx, sid); !open {
		t.Fatal("a submitted plan still gates review")
	}
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
	if _, open := e.plans.OpenPlanOf(e.ctx, sid); open {
		t.Fatal("an approved plan must not gate review")
	}
}

func TestInPlaceAnswersGoToLiveAuthorOrRespawnInPlace(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.author(sid), e.item.Ref(), goodQuestions()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.Defaults(e.ctx, "p1", owner, e.item.Ref(), false); err != nil {
		t.Fatal(err)
	}
	if len(e.sessions.sent) != 1 || len(e.sessions.spawned) != 0 || len(e.sessions.inPlace) != 0 {
		t.Fatalf("live author: sent %d, planners %d, in-place %d", len(e.sessions.sent), len(e.sessions.spawned), len(e.sessions.inPlace))
	}

	// A second item whose author died: answers start an implementer in place.
	e2 := inPlaceEnv(t)
	_, dead := e2.startInPlace()
	if _, err := e2.plans.SubmitQuestions(e2.ctx, "p1", e2.author(dead), e2.item.Ref(), goodQuestions()); err != nil {
		t.Fatal(err)
	}
	e2.sessions.ended[dead] = true
	if _, err := e2.plans.Defaults(e2.ctx, "p1", owner, e2.item.Ref(), false); err != nil {
		t.Fatal(err)
	}
	if len(e2.sessions.spawned) != 0 || len(e2.sessions.inPlace) != 1 {
		t.Fatalf("dead author: planners %d, in-place %d (want 0, 1)", len(e2.sessions.spawned), len(e2.sessions.inPlace))
	}
	if !strings.Contains(e2.sessions.inPlace[0].Prompt, "Publishing:") {
		t.Fatalf("respawn prompt lacks the publishing brief: %q", e2.sessions.inPlace[0].Prompt)
	}
	if !strings.Contains(e2.sessions.inPlace[0].Prompt, "Plan in place") || !strings.Contains(e2.sessions.inPlace[0].Prompt, "Questions and answers so far") {
		t.Fatalf("respawn prompt = %q", e2.sessions.inPlace[0].Prompt)
	}
	cur, _ := e2.plans.Get(e2.ctx, "p1", e2.item.Ref())
	if cur.PlannerSessionID == dead || cur.PlannerSessionID == "" {
		t.Fatalf("the new author was not recorded: %q", cur.PlannerSessionID)
	}
}

func (e *planEnv) submitInPlace(sid domain.SessionID, prs []domain.PlanPR) {
	e.t.Helper()
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.author(sid), e.item.Ref(), backlog.PlanQuestionsInput{}); err != nil {
		e.t.Fatal(err)
	}
	in := goodSubmit()
	in.PRs = prs
	if _, err := e.plans.Submit(e.ctx, "p1", e.author(sid), e.item.Ref(), in); err != nil {
		e.t.Fatal(err)
	}
}

func TestInPlaceApproveSinglePRMessagesAuthor(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	e.submitInPlace(sid, goodSubmit().PRs)
	e.sessions.sent, e.notify.sent = nil, nil
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
	if len(e.sessions.sent) != 1 || !strings.Contains(e.sessions.sent[0], "Approved plan") || !strings.Contains(e.sessions.sent[0], "PR 1 of 1") {
		t.Fatalf("author message = %q", e.sessions.sent)
	}
	if len(e.notify.sent) != 1 || !strings.Contains(e.notify.sent[0], "builds in place") || strings.Contains(e.notify.sent[0], "Spawn the implementer") {
		t.Fatalf("orchestrator note = %q", e.notify.sent)
	}
}

func TestInPlaceApproveSplitLinksAuthorToChildOne(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	e.submitInPlace(sid, []domain.PlanPR{
		{Title: "Backend", Files: []string{"a.go"}, Tests: "TestA", Produces: []string{"A()"}},
		{Title: "UI", Files: []string{"b.ts"}, Tests: "TestB", Consumes: []string{"A()"}},
	})
	e.sessions.sent, e.notify.sent = nil, nil
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
	kids := e.children()
	if len(kids) != 2 {
		t.Fatalf("children = %d", len(kids))
	}
	facts, err := e.store.BacklogFactsFor(e.ctx, kids[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	linked := false
	for _, s := range facts.Sessions {
		linked = linked || s.SessionID == string(sid)
	}
	if !linked {
		t.Fatalf("author %s not linked to child 1: %+v", sid, facts.Sessions)
	}
	if len(e.sessions.sent) != 1 || !strings.Contains(e.sessions.sent[0], "PR 1 of 2") {
		t.Fatalf("author message = %q", e.sessions.sent)
	}
	if len(e.notify.sent) != 1 || !strings.Contains(e.notify.sent[0], kids[1].Ref()) || strings.Contains(e.notify.sent[0], kids[0].Ref()+",") {
		t.Fatalf("orchestrator note = %q", e.notify.sent)
	}
}

func TestInPlaceAuthorEndingWhileDraftingFailsPlanOnce(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	e.notify.sent = nil
	if e.plans.SessionEnded(e.ctx, sid) {
		t.Fatal("an in-place author is a worker: SessionEnded must return false")
	}
	if e.plans.SessionEnded(e.ctx, sid) {
		t.Fatal("second call must also return false")
	}
	cur, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if len(e.notify.sent) == 1 && (!strings.Contains(e.notify.sent[0], "--plan in-place") || strings.Contains(e.notify.sent[0], "plan start")) {
		t.Fatalf("notice must not send the orchestrator to a separate planner: %q", e.notify.sent[0])
	}
	if cur.Status != domain.PlanFailed || len(e.notify.sent) != 1 {
		t.Fatalf("plan %s, notices %d (want failed, 1)", cur.Status, len(e.notify.sent))
	}
}

func TestInPlaceAuthorEndingAfterSubmitKeepsPlan(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	e.submitInPlace(sid, goodSubmit().PRs)
	e.plans.SessionEnded(e.ctx, sid)
	if cur, _ := e.plans.Get(e.ctx, "p1", e.item.Ref()); cur.Status != domain.PlanAwaitingApproval {
		t.Fatalf("plan = %s", cur.Status)
	}
}

func TestInPlaceReplanNeverKillsTheAuthor(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	e.submitInPlace(sid, goodSubmit().PRs)
	e.sessions.sent = nil
	np, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if len(e.sessions.terminated) != 0 {
		t.Fatalf("terminated %v", e.sessions.terminated)
	}
	if np.PlannerSessionID != sid || np.Status != domain.PlanDrafting || len(e.sessions.spawned) != 0 {
		t.Fatalf("new plan %+v, planners %d", np, len(e.sessions.spawned))
	}
	if len(e.sessions.sent) != 1 || !strings.Contains(e.sessions.sent[0], "plan again") {
		t.Fatalf("author message = %q", e.sessions.sent)
	}
}

func TestInPlaceReplanAfterApprovalWithAuthorPRStartsAFreshImplementer(t *testing.T) {
	e := inPlaceEnv(t)
	_, old := e.startInPlace()
	e.submitInPlace(old, goodSubmit().PRs)
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
	pr := domain.PullRequest{URL: "https://github.com/o/r/pull/2", SessionID: old, Number: 2, UpdatedAt: time.Now().UTC()}
	if err := e.store.WritePR(e.ctx, pr, nil, nil); err != nil {
		t.Fatal(err)
	}
	np, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if np.PlannerSessionID == "" || np.PlannerSessionID == old || len(e.sessions.inPlace) != 1 || len(e.sessions.terminated) != 0 {
		t.Fatalf("new author %q (old %q), in-place spawns %d, terminated %v", np.PlannerSessionID, old, len(e.sessions.inPlace), e.sessions.terminated)
	}
	// The fresh implementer can plan: it has no PR.
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.author(np.PlannerSessionID), e.item.Ref(), backlog.PlanQuestionsInput{}); err != nil {
		t.Fatalf("fresh author questions: %v", err)
	}
}

func TestInPlaceItemClosedNeverKillsTheAuthor(t *testing.T) {
	e := inPlaceEnv(t)
	plan, _ := e.startInPlace()
	e.plans.ItemClosed(e.ctx, e.item.ID)
	if len(e.sessions.terminated) != 0 {
		t.Fatalf("terminated %v", e.sessions.terminated)
	}
	if cur, _, _ := e.store.GetBacklogPlan(e.ctx, plan.ID); cur.Status != domain.PlanSuperseded {
		t.Fatalf("plan = %s, want superseded", cur.Status)
	}
}

func TestInPlaceSplitApprovalWithEndedAuthorNamesEveryChild(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	e.submitInPlace(sid, []domain.PlanPR{
		{Title: "Backend", Files: []string{"a.go"}, Tests: "TestA", Produces: []string{"A()"}},
		{Title: "UI", Files: []string{"b.ts"}, Tests: "TestB", Consumes: []string{"A()"}},
	})
	e.sessions.ended[sid] = true
	e.notify.sent = nil
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
	kids := e.children()
	if len(e.notify.sent) != 1 || !strings.Contains(e.notify.sent[0], kids[0].Ref()) || !strings.Contains(e.notify.sent[0], kids[1].Ref()) || !strings.Contains(e.notify.sent[0], "ended") {
		t.Fatalf("orchestrator note = %q", e.notify.sent)
	}
}

func TestInPlaceApproveWithEndedAuthorAsksForASpawn(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	e.submitInPlace(sid, goodSubmit().PRs)
	e.sessions.ended[sid] = true
	e.notify.sent = nil
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
	if len(e.notify.sent) != 1 || !strings.Contains(e.notify.sent[0], "ended") || !strings.Contains(e.notify.sent[0], "tallpine spawn --backlog "+e.item.Ref()) {
		t.Fatalf("orchestrator note = %q", e.notify.sent)
	}
}
