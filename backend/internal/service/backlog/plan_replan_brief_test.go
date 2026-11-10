package backlog_test

import (
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
)

func TestInPlaceReplanBriefsTheAuthorWithTheSupersededPlan(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	e.submitInPlace(sid, goodSubmit().PRs)
	old, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	e.sessions.sent = nil
	if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	if len(e.sessions.sent) != 1 {
		t.Fatalf("sent %q", e.sessions.sent)
	}
	for _, want := range []string{"Superseded plan " + old.ID, "who pays | each friend", "Backend", "--plan-id " + old.ID} {
		if !strings.Contains(e.sessions.sent[0], want) {
			t.Fatalf("author message lacks %q:\n%s", want, e.sessions.sent[0])
		}
	}
	got, err := e.plans.GetByID(e.ctx, "p1", e.item.Ref(), old.ID)
	if err != nil || got.Status != domain.PlanSuperseded {
		t.Fatalf("GetByID = %+v, %v", got, err)
	}
	if _, err := e.plans.GetByID(e.ctx, "p1", e.item.Ref(), "nope"); err == nil {
		t.Fatal("unknown plan id must be refused")
	}
}

func TestReplan_NewPlannerGetsTheSupersededPlan(t *testing.T) {
	e := approvedWith(t, "Backend")
	e.sessions.spawned = nil
	e.replanTo(prs("Backend"))
	if len(e.sessions.spawned) != 1 {
		t.Fatalf("spawned %d planners", len(e.sessions.spawned))
	}
	p := e.sessions.spawned[0].Prompt
	for _, want := range []string{"Superseded plan", "who pays | each friend", "--plan-id"} {
		if !strings.Contains(p, want) {
			t.Fatalf("replanner brief lacks %q:\n%s", want, p)
		}
	}
}

func TestReplan_UnansweredQuestionsPlanBriefsWithoutPanic(t *testing.T) {
	e := newPlanEnv(t, true)
	old := e.mustStartWithQuestions()
	e.sessions.spawned = nil
	if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	p := e.sessions.spawned[0].Prompt
	if !strings.Contains(p, "Superseded plan "+old.ID) || !strings.Contains(p, "Q q1:") || !strings.Contains(p, "seats hold 10 minutes") {
		t.Fatalf("brief lacks the unanswered questions:\n%s", p)
	}
	if strings.Contains(p, "Full plan: tallpine plan show") {
		t.Fatalf("brief points at the new draft:\n%s", p)
	}
}

func TestInPlaceReplan_UnansweredQuestionsPlanBriefsWithoutPanic(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.author(sid), e.item.Ref(), goodQuestions()); err != nil {
		t.Fatal(err)
	}
	e.sessions.sent = nil
	if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	if len(e.sessions.sent) != 1 || !strings.Contains(e.sessions.sent[0], "Q q1:") {
		t.Fatalf("author message = %q", e.sessions.sent)
	}
}

func TestReplanTwice_SecondPlannerStillGetsTheOriginalPlan(t *testing.T) {
	e := approvedWith(t, "Backend")
	orig, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	e.sessions.spawned = nil
	for i := 0; i < 2; i++ {
		if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.sessions.spawned) != 2 {
		t.Fatalf("spawned %d planners", len(e.sessions.spawned))
	}
	p := e.sessions.spawned[1].Prompt
	if !strings.Contains(p, "Superseded plan "+orig.ID) || !strings.Contains(p, "who pays | each friend") {
		t.Fatalf("second replanner lacks the original plan:\n%s", p)
	}
}

func TestInPlaceReplanTwice_AuthorStillGetsTheOriginalPlan(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	e.submitInPlace(sid, goodSubmit().PRs)
	orig, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	e.sessions.sent = nil
	for i := 0; i < 2; i++ {
		if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.sessions.sent) != 2 || !strings.Contains(e.sessions.sent[1], "Superseded plan "+orig.ID) {
		t.Fatalf("second author message lacks the original plan: %q", e.sessions.sent)
	}
}

func TestReplan_QuestionsOnlyIntermediatePlanKeepsTheOriginal(t *testing.T) {
	e := approvedWith(t, "Backend")
	orig, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	b, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(b), e.item.Ref(), goodQuestions()); err != nil {
		t.Fatal(err)
	}
	e.sessions.spawned = nil
	if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	p := e.sessions.spawned[0].Prompt
	for _, want := range []string{"Superseded plan " + orig.ID, "who pays | each friend", "Q q1:"} {
		if !strings.Contains(p, want) {
			t.Fatalf("brief lacks %q:\n%s", want, p)
		}
	}
}

func TestInPlaceReplan_QuestionsOnlyIntermediatePlanKeepsTheOriginal(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	e.submitInPlace(sid, goodSubmit().PRs)
	orig, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.author(sid), e.item.Ref(), goodQuestions()); err != nil {
		t.Fatal(err)
	}
	e.sessions.sent = nil
	if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	m := e.sessions.sent[0]
	for _, want := range []string{"Superseded plan " + orig.ID, "who pays | each friend", "Q q1:"} {
		if !strings.Contains(m, want) {
			t.Fatalf("author message lacks %q:\n%s", want, m)
		}
	}
}

func TestReplan_OversizedPlanBriefStaysWithinTheCap(t *testing.T) {
	e := approvedWith(t, "Backend")
	in := goodSubmit()
	in.Plan = strings.Repeat("x", 50000)
	cur, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	e.store.UpdateBacklogPlan(e.ctx, cur.ID, func(pl *domain.BacklogPlan) error { pl.PlanMarkdown = in.Plan; return nil })
	e.sessions.spawned = nil
	if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	p := e.sessions.spawned[0].Prompt
	if n := len([]rune(p)); n > 20000 || !strings.Contains(p, "[truncated]") || !strings.Contains(p, "who pays | each friend") {
		t.Fatalf("prompt = %d runes, truncated=%v", n, strings.Contains(p, "[truncated]"))
	}
}

func TestReplan_AnswerToEndedReplannerStillBriefsTheOriginal(t *testing.T) {
	e := approvedWith(t, "Backend")
	orig, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	b, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(b), e.item.Ref(), goodQuestions()); err != nil {
		t.Fatal(err)
	}
	e.sessions.ended[b.PlannerSessionID] = true
	e.sessions.spawned = nil
	if _, err := e.plans.Defaults(e.ctx, "p1", owner, e.item.Ref(), false); err != nil {
		t.Fatal(err)
	}
	if len(e.sessions.spawned) != 1 {
		t.Fatalf("spawned %d", len(e.sessions.spawned))
	}
	p := e.sessions.spawned[0].Prompt
	for _, want := range []string{"Superseded plan " + orig.ID, "who pays | each friend", "do not ask again"} {
		if !strings.Contains(p, want) {
			t.Fatalf("respawn prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Count(p, "Q q1:") != 1 {
		t.Fatalf("questions repeated:\n%s", p)
	}
}

func TestInPlaceReplan_AnswerToEndedAuthorStillBriefsTheOriginal(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	e.submitInPlace(sid, goodSubmit().PRs)
	orig, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.author(sid), e.item.Ref(), goodQuestions()); err != nil {
		t.Fatal(err)
	}
	e.sessions.ended[sid] = true
	e.sessions.inPlace = nil
	if _, err := e.plans.Defaults(e.ctx, "p1", owner, e.item.Ref(), false); err != nil {
		t.Fatal(err)
	}
	if len(e.sessions.inPlace) != 1 || !strings.Contains(e.sessions.inPlace[0].Prompt, "Superseded plan "+orig.ID) {
		t.Fatalf("respawn = %+v", e.sessions.inPlace)
	}
}

func TestCancelThenStart_AnswerToEndedPlannerGetsNoSupersededBrief(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.mustStart()
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), backlog.PlanQuestionsInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.Submit(e.ctx, "p1", e.planner(p), e.item.Ref(), goodSubmit()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.Cancel(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	b := e.mustStartWithQuestions()
	e.sessions.ended[b.PlannerSessionID] = true
	e.sessions.spawned = nil
	if _, err := e.plans.Defaults(e.ctx, "p1", owner, e.item.Ref(), false); err != nil {
		t.Fatal(err)
	}
	if len(e.sessions.spawned) != 1 || strings.Contains(e.sessions.spawned[0].Prompt, "Superseded plan") {
		t.Fatalf("respawn after a cancelled plan = %+v, want no superseded brief", e.sessions.spawned)
	}
}

func TestCancelledReplanEndsTheLineage(t *testing.T) {
	e := approvedWith(t, "Backend")
	if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err != nil { // A superseded by B
		t.Fatal(err)
	}
	if _, err := e.plans.Cancel(e.ctx, "p1", owner, e.item.Ref()); err != nil { // B cancelled
		t.Fatal(err)
	}
	c := e.mustStartWithQuestions()
	e.sessions.ended[c.PlannerSessionID] = true
	e.sessions.spawned = nil
	if _, err := e.plans.Defaults(e.ctx, "p1", owner, e.item.Ref(), false); err != nil {
		t.Fatal(err)
	}
	if len(e.sessions.spawned) != 1 || strings.Contains(e.sessions.spawned[0].Prompt, "Superseded plan") {
		t.Fatalf("respawn behind a cancelled replan = %+v, want no superseded brief", e.sessions.spawned)
	}
}

func TestStartAfterFailedReplanKeepsTheOriginalPlan(t *testing.T) {
	e := approvedWith(t, "Backend")
	orig, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	b, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref())
	if err != nil {
		t.Fatal(err)
	}
	e.sessions.ended[b.PlannerSessionID] = true
	e.plans.SessionEnded(e.ctx, b.PlannerSessionID) // B fails without submitting
	e.sessions.spawned = nil
	if _, _, err := e.plans.Start(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	if len(e.sessions.spawned) != 1 || !strings.Contains(e.sessions.spawned[0].Prompt, "Superseded plan "+orig.ID) {
		t.Fatalf("retry planner = %+v, want the original plan", e.sessions.spawned)
	}
}

func TestInPlaceSpawnAfterFailedReplanKeepsTheOriginalPlan(t *testing.T) {
	e := inPlaceEnv(t)
	_, sid := e.startInPlace()
	e.submitInPlace(sid, goodSubmit().PRs)
	orig, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	e.sessions.ended[sid] = true
	e.plans.SessionEnded(e.ctx, sid)
	sp, err := e.bl.PrepareSpawnPlan(e.ctx, "p1", e.item.Ref(), false, true, false, "")
	if err != nil || !strings.Contains(sp.Context, "Superseded plan "+orig.ID) {
		t.Fatalf("PrepareSpawnPlan = %+v, %v; want the original plan in its context", sp, err)
	}
}

func TestReplanTwice_QuestionsOnlyPlanKeepsItsAnswers(t *testing.T) {
	e := newPlanEnv(t, true)
	a := e.mustStartWithQuestions()
	if _, err := e.plans.Defaults(e.ctx, "p1", owner, e.item.Ref(), false); err != nil {
		t.Fatal(err)
	}
	e.sessions.spawned = nil
	for i := 0; i < 2; i++ { // A -> empty B -> empty C
		if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err != nil {
			t.Fatal(err)
		}
	}
	p := e.sessions.spawned[1].Prompt
	for _, want := range []string{"Superseded plan " + a.ID, "Q q1:", "Answer:"} {
		if !strings.Contains(p, want) {
			t.Fatalf("second replanner lacks %q:\n%s", want, p)
		}
	}
}
