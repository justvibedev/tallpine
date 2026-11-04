package backlog_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// planStore wraps the realStore store and scripts the two chat-message lookups, so a
// test can say who wrote what and when without a whole conversation.
type planStore struct {
	*sqlite.Store
	messages map[string]store.PlanMessageSource
	latest   string
	latestAt time.Time
}

func (s *planStore) BacklogPlanMessageSource(_ context.Context, id string) (store.PlanMessageSource, bool, error) {
	m, ok := s.messages[id]
	return m, ok, nil
}

func (s *planStore) LatestHumanProjectMessage(_ context.Context, _ domain.ProjectID) (string, time.Time, bool, error) {
	return s.latest, s.latestAt, s.latest != "", nil
}

type planEnv struct {
	t        *testing.T
	ctx      context.Context
	store    *planStore
	bl       *backlog.Service
	plans    *backlog.PlanService
	sessions *fakeSessions
	notify   *planNotifier
	now      time.Time
	item     backlog.View
}

var (
	owner    = backlog.Actor{Kind: domain.BacklogOriginHuman}
	planOrch = backlog.Actor{Kind: domain.BacklogOriginOrchestrator, SessionID: "orch-1"}
)

func newPlanEnv(t *testing.T, enabled bool) *planEnv {
	t.Helper()
	return newPlanEnvWith(t, enabled, nil)
}

// newPlanEnvWith is newPlanEnv with a hook to set optional plan dependencies.
func newPlanEnvWith(t *testing.T, enabled bool, tune func(*backlog.PlanDeps)) *planEnv {
	t.Helper()
	realStore := sqlitetest.MustOpen(t)
	ctx := context.Background()
	if err := realStore.UpsertProject(ctx, domain.ProjectRecord{ID: "p1", Path: "/tmp/p1", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	ps := &planStore{Store: realStore, messages: map[string]store.PlanMessageSource{}}
	e := &planEnv{t: t, ctx: ctx, store: ps, sessions: &fakeSessions{store: realStore, ended: map[domain.SessionID]bool{}}, notify: &planNotifier{}, now: time.Now().UTC()}
	e.bl = backlog.New(backlog.Deps{Store: realStore})
	deps := backlog.PlanDeps{
		Backlog: e.bl, Store: ps, Sessions: e.sessions, Notify: e.notify, Enabled: enabled,
		Now: func() time.Time { e.now = e.now.Add(time.Second); return e.now },
	}
	if tune != nil {
		tune(&deps)
	}
	e.plans = backlog.NewPlanService(deps)
	v, err := e.bl.Create(ctx, "p1", owner, backlog.CreateInput{Title: "Add seat sharing", Note: "let friends pay", DoneWhen: "friends pay own seat"})
	if err != nil {
		t.Fatal(err)
	}
	e.item = v
	return e
}

func (e *planEnv) mustStart() domain.BacklogPlan {
	e.t.Helper()
	p, created, err := e.plans.Start(e.ctx, "p1", owner, e.item.Ref())
	if err != nil || !created {
		e.t.Fatalf("Start = %v, created %v", err, created)
	}
	return p
}

// mustStartWithQuestions starts the plan and has its planner submit the good
// questions.
func (e *planEnv) mustStartWithQuestions() domain.BacklogPlan {
	e.t.Helper()
	p := e.mustStart()
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), goodQuestions()); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *planEnv) planner(p domain.BacklogPlan) backlog.Actor {
	return backlog.Actor{Kind: domain.BacklogOriginWorker, SessionID: string(p.PlannerSessionID)}
}

func question(id string) domain.PlanQuestion {
	return domain.PlanQuestion{ID: id, Text: "Who pays?", Reason: "money", Recommended: "a",
		Options: []domain.PlanOption{{ID: "a", Label: "Each friend"}, {ID: "b", Label: "The booker"}}}
}

func goodQuestions() backlog.PlanQuestionsInput {
	return backlog.PlanQuestionsInput{
		Questions:   []domain.PlanQuestion{question("q1")},
		Assumptions: []domain.PlanAssumption{{ID: "s1", Text: "Assumed: seats hold 10 minutes"}},
	}
}

func goodSubmit() backlog.PlanSubmitInput {
	return backlog.PlanSubmitInput{
		Plan:      "# Plan\n\nGoal.",
		Decisions: "| q | a |\n|---|---|\n| who pays | each friend |",
		PRs:       []domain.PlanPR{{Title: "Backend", Files: []string{"a.go"}, Tests: "TestX", Produces: []string{"Seat.Share()"}}},
		Focus:     []string{"double charge"},
	}
}

// toApproval drives a plan to awaiting_approval.
func (e *planEnv) toApproval() domain.BacklogPlan {
	e.t.Helper()
	p := e.mustStart()
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), goodQuestions()); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.plans.Defaults(e.ctx, "p1", owner, e.item.Ref(), false); err != nil {
		e.t.Fatal(err)
	}
	p, err := e.plans.Submit(e.ctx, "p1", e.planner(p), e.item.Ref(), goodSubmit())
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func TestPlan_RejectsSixQuestions(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.mustStart()
	in := backlog.PlanQuestionsInput{}
	for i := 0; i < 6; i++ {
		in.Questions = append(in.Questions, question(fmt.Sprintf("q%d", i)))
	}
	_, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), in)
	if apiCode(t, err) != "PLAN_INVALID" || !strings.Contains(err.Error(), "at most 5 questions") {
		t.Fatalf("err = %v, want the 5-question cap", err)
	}
}

func TestPlan_RejectsTwentyOneAssumptions(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.mustStart()
	in := backlog.PlanQuestionsInput{}
	for i := 0; i < 21; i++ {
		in.Assumptions = append(in.Assumptions, domain.PlanAssumption{ID: fmt.Sprintf("s%d", i), Text: "Assumed: x"})
	}
	_, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), in)
	if apiCode(t, err) != "PLAN_INVALID" || !strings.Contains(err.Error(), "at most 20 assumptions") {
		t.Fatalf("err = %v, want the 20-assumption cap", err)
	}
}

func TestPlan_SubmitRejectsSixFocusItems(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.mustStart()
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), backlog.PlanQuestionsInput{}); err != nil {
		t.Fatal(err)
	}
	in := goodSubmit()
	in.Focus = []string{"1", "2", "3", "4", "5", "6"}
	_, err := e.plans.Submit(e.ctx, "p1", e.planner(p), e.item.Ref(), in)
	if apiCode(t, err) != "PLAN_INVALID" || !strings.Contains(err.Error(), "review focus") {
		t.Fatalf("err = %v, want the focus cap", err)
	}
	in = goodSubmit()
	in.PRs[0].Produces = nil
	if _, err := e.plans.Submit(e.ctx, "p1", e.planner(p), e.item.Ref(), in); apiCode(t, err) != "PLAN_INVALID" {
		t.Fatalf("PR entry without produces/consumes: err = %v", err)
	}
}

func TestPlan_OnlyRecordedPlannerSubmits(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.mustStart()
	other := backlog.Actor{Kind: domain.BacklogOriginWorker, SessionID: "someone-else"}
	for name, actor := range map[string]backlog.Actor{"another worker": other, "orchestrator": planOrch, "owner": owner} {
		if _, err := e.plans.SubmitQuestions(e.ctx, "p1", actor, e.item.Ref(), goodQuestions()); apiCode(t, err) != "PLAN_NOT_PLANNER" {
			t.Errorf("%s: err = %v, want PLAN_NOT_PLANNER", name, err)
		}
	}
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), goodQuestions()); err != nil {
		t.Fatalf("recorded planner: %v", err)
	}
}

func TestPlan_StateTransitions(t *testing.T) {
	all := []domain.PlanStatus{domain.PlanDrafting, domain.PlanAwaitingAnswers, domain.PlanWriting, domain.PlanAwaitingApproval, domain.PlanApproved, domain.PlanSuperseded, domain.PlanFailed}
	allowed := map[[2]domain.PlanStatus]bool{}
	for _, tr := range [][2]domain.PlanStatus{
		{domain.PlanDrafting, domain.PlanAwaitingAnswers}, {domain.PlanDrafting, domain.PlanWriting},
		{domain.PlanAwaitingAnswers, domain.PlanWriting}, {domain.PlanWriting, domain.PlanAwaitingApproval},
		{domain.PlanAwaitingApproval, domain.PlanApproved}, {domain.PlanApproved, domain.PlanSuperseded}, {domain.PlanFailed, domain.PlanSuperseded},
	} {
		allowed[tr] = true
	}
	for _, from := range []domain.PlanStatus{domain.PlanDrafting, domain.PlanAwaitingAnswers, domain.PlanWriting, domain.PlanAwaitingApproval} {
		allowed[[2]domain.PlanStatus{from, domain.PlanFailed}] = true
		allowed[[2]domain.PlanStatus{from, domain.PlanSuperseded}] = true
	}
	for _, from := range all {
		for _, to := range all {
			if got := from.CanTransition(to); got != allowed[[2]domain.PlanStatus{from, to}] {
				t.Errorf("%s -> %s: CanTransition = %v, want %v", from, to, got, !got)
			}
		}
	}

	// The service refuses out-of-order calls with a clear error.
	e := newPlanEnv(t, true)
	p := e.mustStart()
	if _, err := e.plans.Submit(e.ctx, "p1", e.planner(p), e.item.Ref(), goodSubmit()); apiCode(t, err) != "PLAN_WRONG_STATE" {
		t.Errorf("submit before questions: err = %v", err)
	}
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); apiCode(t, err) != "PLAN_WRONG_STATE" {
		t.Errorf("approve while drafting: err = %v", err)
	}
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), goodQuestions()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), goodQuestions()); apiCode(t, err) != "PLAN_WRONG_STATE" {
		t.Errorf("questions twice: err = %v", err)
	}
}

func TestPlan_NoQuestionsGoesStraightToWriting(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.mustStart()
	got, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), backlog.PlanQuestionsInput{})
	if err != nil || got.Status != domain.PlanWriting {
		t.Fatalf("status = %s, err %v; want writing", got.Status, err)
	}
}

func TestPlan_OneOpenPlanPerItem(t *testing.T) {
	e := newPlanEnv(t, true)
	first := e.mustStart()
	again, created, err := e.plans.Start(e.ctx, "p1", owner, e.item.Ref())
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("second Start = %+v created %v err %v; want the open plan", again, created, err)
	}
	if n := len(e.sessions.spawned); n != 1 {
		t.Fatalf("planner sessions started = %d, want 1", n)
	}
	// The unique index holds below the service as well.
	_, created, err = e.store.CreateBacklogPlan(e.ctx, domain.BacklogPlan{
		ID: "plan_dup", ProjectID: "p1", ItemID: e.item.ID, Status: domain.PlanDrafting, ItemUpdatedAt: e.now, StartedAt: e.now, UpdatedAt: e.now,
	})
	if err != nil || created {
		t.Fatalf("store duplicate: created %v err %v; want the open plan back", created, err)
	}
}

func TestPlan_OrchestratorApproveNeedsOwnerMessage(t *testing.T) {
	e := newPlanEnv(t, true)
	e.toApproval()
	if _, err := e.plans.Approve(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{}); apiCode(t, err) != "PLAN_OWNER_MESSAGE_REQUIRED" {
		t.Fatalf("no message: err = %v", err)
	}
	// A message the owner did not write does not count.
	e.store.messages["m-agent"] = store.PlanMessageSource{Human: false, ProjectID: "p1", At: e.now.Add(time.Hour)}
	if _, err := e.plans.Approve(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{OwnerMessage: "m-agent"}); apiCode(t, err) != "PLAN_OWNER_MESSAGE_INVALID" {
		t.Fatalf("agent message: err = %v", err)
	}
	if _, err := e.plans.Approve(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{OwnerMessage: "nope"}); apiCode(t, err) != "PLAN_OWNER_MESSAGE_INVALID" {
		t.Fatalf("unknown message: err = %v", err)
	}
	e.store.messages["m-other-project"] = store.PlanMessageSource{Human: true, ProjectID: "p2", At: e.now.Add(time.Hour)}
	if _, err := e.plans.Approve(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{OwnerMessage: "m-other-project"}); apiCode(t, err) != "PLAN_OWNER_MESSAGE_INVALID" {
		t.Fatalf("other project: err = %v", err)
	}
	e.store.messages["m-ok"] = store.PlanMessageSource{Human: true, ProjectID: "p1", At: e.now.Add(time.Hour)}
	got, err := e.plans.Approve(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{OwnerMessage: "m-ok"})
	if err != nil || got.Status != domain.PlanApproved || got.OwnerMessageID != "m-ok" || got.ApprovedBy != "orchestrator:orch-1" {
		t.Fatalf("approve = %+v, %v", got, err)
	}
}

func TestPlan_OrchestratorCannotApproveWithStaleOwnerMessage(t *testing.T) {
	e := newPlanEnv(t, true)
	e.toApproval()
	e.store.messages["m-old"] = store.PlanMessageSource{Human: true, ProjectID: "p1", At: e.now.Add(-time.Hour)}
	if _, err := e.plans.Approve(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{OwnerMessage: "m-old"}); apiCode(t, err) != "PLAN_OWNER_MESSAGE_STALE" {
		t.Fatalf("old message: err = %v", err)
	}
	// "last" resolves to the owner's newest message, which must also be newer.
	e.store.latest, e.store.latestAt = "m-old", e.now.Add(-time.Hour)
	if _, err := e.plans.Approve(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{OwnerMessage: "last"}); apiCode(t, err) != "PLAN_OWNER_MESSAGE_STALE" {
		t.Fatalf("stale last: err = %v", err)
	}
	e.store.latest, e.store.latestAt = "m-new", e.now.Add(time.Hour)
	if got, err := e.plans.Approve(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{OwnerMessage: "last"}); err != nil || got.OwnerMessageID != "m-new" {
		t.Fatalf("fresh last: %+v, %v", got, err)
	}
}

func TestPlan_OwnerApprovesDirectly(t *testing.T) {
	e := newPlanEnv(t, true)
	e.toApproval()
	got, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{})
	if err != nil || got.ApprovedBy != "user" || got.Status != domain.PlanApproved {
		t.Fatalf("owner approve = %+v, %v", got, err)
	}
	for _, actor := range []backlog.Actor{{Kind: domain.BacklogOriginWorker, SessionID: "w"}, {Kind: domain.BacklogOriginReviewer, SessionID: "r"}} {
		if _, err := e.plans.Approve(e.ctx, "p1", actor, e.item.Ref(), backlog.PlanApproveInput{}); apiCode(t, err) != "PLAN_ACTOR_FORBIDDEN" {
			t.Errorf("%s approve: err = %v", actor.Kind, err)
		}
	}
}

func TestPlan_RelayedAnswersMarked(t *testing.T) {
	e := newPlanEnv(t, true)
	e.mustStartWithQuestions()
	in := backlog.PlanAnswerInput{
		Questions:   map[string]domain.PlanAnswer{"q1": {Other: "split by seat price"}},
		Corrections: map[string]string{"s1": "15 minutes"},
	}
	// An orchestrator cannot pass answers off as the owner's, even with Relayed false.
	got, err := e.plans.Answer(e.ctx, "p1", planOrch, e.item.Ref(), in)
	if err != nil {
		t.Fatal(err)
	}
	if a := got.Answers; a == nil || !a.Relayed || a.By != "orchestrator:orch-1" || a.Corrections["s1"] != "15 minutes" {
		t.Fatalf("answers = %+v, want relayed by the orchestrator", a)
	}
	if len(e.sessions.sent) != 1 || !strings.Contains(e.sessions.sent[0], "split by seat price") || !strings.Contains(e.sessions.sent[0], "15 minutes") {
		t.Fatalf("planner got %q", e.sessions.sent)
	}
	// The owner's own answer is not relayed.
	e2 := newPlanEnv(t, true)
	p2 := e2.mustStart()
	if _, err := e2.plans.SubmitQuestions(e2.ctx, "p1", e2.planner(p2), e2.item.Ref(), goodQuestions()); err != nil {
		t.Fatal(err)
	}
	got, err = e2.plans.Answer(e2.ctx, "p1", owner, e2.item.Ref(), backlog.PlanAnswerInput{Questions: map[string]domain.PlanAnswer{"q1": {Option: "b"}}, Relayed: true})
	if err != nil || got.Answers.Relayed || got.Answers.By != "user" {
		t.Fatalf("owner answers = %+v, %v", got.Answers, err)
	}
}

func TestPlan_AnswersAreChecked(t *testing.T) {
	e := newPlanEnv(t, true)
	e.mustStartWithQuestions()
	for name, in := range map[string]backlog.PlanAnswerInput{
		"missing answer":     {Questions: map[string]domain.PlanAnswer{}},
		"unknown option":     {Questions: map[string]domain.PlanAnswer{"q1": {Option: "zz"}}},
		"unknown question":   {Questions: map[string]domain.PlanAnswer{"q1": {Option: "a"}, "q9": {Option: "a"}}},
		"unknown assumption": {Questions: map[string]domain.PlanAnswer{"q1": {Option: "a"}}, Corrections: map[string]string{"s9": "x"}},
	} {
		if _, err := e.plans.Answer(e.ctx, "p1", owner, e.item.Ref(), in); apiCode(t, err) != "PLAN_INVALID" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if cur, _ := e.plans.Get(e.ctx, "p1", e.item.Ref()); cur.Status != domain.PlanAwaitingAnswers {
		t.Fatalf("a rejected answer moved the plan to %s", cur.Status)
	}
}

func TestPlan_DefaultsAcceptRecommendations(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.mustStart()
	in := goodQuestions()
	in.Questions = append(in.Questions, domain.PlanQuestion{ID: "q2", Text: "Expiry?", Recommended: "y",
		Options: []domain.PlanOption{{ID: "x", Label: "5 min"}, {ID: "y", Label: "10 min"}}})
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), in); err != nil {
		t.Fatal(err)
	}
	got, err := e.plans.Defaults(e.ctx, "p1", owner, e.item.Ref(), false)
	if err != nil || got.Status != domain.PlanWriting {
		t.Fatalf("Defaults = %s, %v", got.Status, err)
	}
	a := got.Answers
	if a == nil || !a.Defaults || a.Questions["q1"].Option != "a" || a.Questions["q2"].Option != "y" || len(a.Corrections) != 0 {
		t.Fatalf("answers = %+v, want every recommendation and no correction", a)
	}
	if !strings.Contains(e.sessions.sent[0], "Accepted.") {
		t.Fatalf("planner message does not mention the accepted assumption: %q", e.sessions.sent[0])
	}
}

func TestPlan_AnswerToEndedPlannerSpawnsNewOne(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.mustStartWithQuestions()
	e.sessions.ended[p.PlannerSessionID] = true
	got, err := e.plans.Defaults(e.ctx, "p1", owner, e.item.Ref(), false)
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if len(e.sessions.spawned) != 2 || cur.PlannerSessionID == p.PlannerSessionID || cur.PlannerSessionID == "" {
		t.Fatalf("planner = %q after respawn (was %q), spawns %d; got status %s", cur.PlannerSessionID, p.PlannerSessionID, len(e.sessions.spawned), got.Status)
	}
	if !strings.Contains(e.sessions.spawned[1].Prompt, "Who pays?") || !strings.Contains(e.sessions.spawned[1].Prompt, "do not ask again") {
		t.Fatalf("new planner's brief lacks the record: %q", e.sessions.spawned[1].Prompt)
	}
}

func TestPlan_NotificationsOncePerTransition(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.mustStartWithQuestions()
	if _, err := e.plans.Defaults(e.ctx, "p1", owner, e.item.Ref(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.Submit(e.ctx, "p1", e.planner(p), e.item.Ref(), goodSubmit()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
	if n := len(e.notify.sent); n != 3 { // questions, plan ready, approved
		t.Fatalf("notifications = %d (%q), want 3", n, e.notify.sent)
	}
}

func TestPlan_PlannerEndedWithoutSubmittingFailsPlan(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.mustStart()
	if !e.plans.SessionEnded(e.ctx, p.PlannerSessionID) {
		t.Fatal("SessionEnded did not recognise the planner")
	}
	cur, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if cur.Status != domain.PlanFailed || cur.Failure == "" || len(e.notify.sent) != 1 {
		t.Fatalf("plan = %s %q, notifications %d", cur.Status, cur.Failure, len(e.notify.sent))
	}
	// Again changes nothing and tells nobody twice.
	e.plans.SessionEnded(e.ctx, p.PlannerSessionID)
	if len(e.notify.sent) != 1 {
		t.Fatalf("second end notified again: %q", e.notify.sent)
	}
	// A plan that waits for the owner is not failed by the planner idling out.
	e2 := newPlanEnv(t, true)
	p2 := e2.mustStart()
	if _, err := e2.plans.SubmitQuestions(e2.ctx, "p1", e2.planner(p2), e2.item.Ref(), goodQuestions()); err != nil {
		t.Fatal(err)
	}
	e2.plans.SessionEnded(e2.ctx, p2.PlannerSessionID)
	if cur, _ := e2.plans.Get(e2.ctx, "p1", e2.item.Ref()); cur.Status != domain.PlanAwaitingAnswers {
		t.Fatalf("awaiting plan became %s", cur.Status)
	}
}

func TestPlan_ReplanSupersedesAndKeepsHistory(t *testing.T) {
	e := newPlanEnv(t, true)
	old := e.toApproval()
	fresh, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref())
	if err != nil || fresh.ID == old.ID || fresh.Status != domain.PlanDrafting {
		t.Fatalf("Replan = %+v, %v", fresh, err)
	}
	got, ok, _ := e.store.GetBacklogPlan(e.ctx, old.ID)
	if !ok || got.Status != domain.PlanSuperseded || got.PlanMarkdown == "" {
		t.Fatalf("old plan = %s (kept text: %v), want superseded with history", got.Status, got.PlanMarkdown != "")
	}
}

func TestPlan_DisabledAnswersNotFound(t *testing.T) {
	e := newPlanEnv(t, false)
	if _, _, err := e.plans.Start(e.ctx, "p1", owner, e.item.Ref()); apiCode(t, err) != "PLANNER_DISABLED" {
		t.Fatalf("Start with the switch off: err = %v", err)
	}
	if e.plans.Enabled() {
		t.Fatal("Enabled() = true with the switch off")
	}
}

// A planner's done report completes the plan, not the item it is linked to.
func TestPlanner_DoneReportDoesNotCloseItem(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.toApproval()
	rep := domain.ReportRecord{SessionID: p.PlannerSessionID, State: domain.ReportDone}
	if err := e.bl.ReportFiled(e.ctx, rep, nil); err != nil {
		t.Fatal(err)
	}
	v, err := e.bl.Get(e.ctx, "p1", e.item.ID)
	if err != nil || v.State != domain.BacklogOpen {
		t.Fatalf("item = %s, %v; a planner's done report must leave it open", v.State, err)
	}
	// Control: an ordinary worker's done report on the same item still closes it.
	now := time.Now().UTC()
	worker, err := e.store.CreateSession(e.ctx, domain.SessionRecord{ProjectID: "p1", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}, Metadata: domain.SessionMetadata{Branch: "b", WorkspacePath: "/w"}, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.bl.LinkSession(e.ctx, e.item.ID, worker.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if err := e.bl.ReportFiled(e.ctx, domain.ReportRecord{SessionID: worker.ID, State: domain.ReportDone}, nil); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.bl.Get(e.ctx, "p1", e.item.ID); v.State != domain.BacklogDone {
		t.Fatalf("worker done report left the item %s, want done", v.State)
	}
}

func TestPlanner_EndedSessionAddsNoWorkerNote(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.mustStart()
	rec, _, _ := e.store.GetSession(e.ctx, p.PlannerSessionID)
	rec.IsTerminated = true
	rec.UpdatedAt = time.Now().UTC()
	if err := e.store.UpdateSession(e.ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := e.bl.Reconcile(e.ctx, e.item.ID); err != nil {
		t.Fatal(err)
	}
	v, _ := e.bl.Get(e.ctx, "p1", e.item.ID)
	if strings.Contains(v.AgentNotes, "ended without merge") || v.State != domain.BacklogOpen {
		t.Fatalf("item = %s notes %q; a planner ending is not an abandoned worker", v.State, v.AgentNotes)
	}
}

func TestSpawnContext_IncludesApprovedDecisions(t *testing.T) {
	e := newPlanEnv(t, true)
	// Not approved yet: the worker's task carries no plan.
	e.toApproval()
	item, err := e.bl.PrepareSpawn(e.ctx, "p1", e.item.Ref(), false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(item.Context, "Approved plan") {
		t.Fatalf("an unapproved plan reached the worker: %q", item.Context)
	}
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
	item, err = e.bl.PrepareSpawn(e.ctx, "p1", e.item.Ref(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Approved plan", "who pays | each friend", "1. Backend", "Seat.Share()", "tallpine plan show " + e.item.Ref(), "Add seat sharing"} {
		if !strings.Contains(item.Context, want) {
			t.Errorf("worker context lacks %q:\n%s", want, item.Context)
		}
	}
	// With the switch off the plan stays hidden.
	off := newPlanEnv(t, false)
	if item, err := off.bl.PrepareSpawn(off.ctx, "p1", off.item.Ref(), false); err != nil || strings.Contains(item.Context, "Approved plan") {
		t.Fatalf("switch off: %v %q", err, item.Context)
	}
}

func TestPlanner_PlanSurvivesArtifactRetention(t *testing.T) {
	e := newPlanEnv(t, true)
	e.toApproval()
	// Retention removes the planner session's artifact directory; the plan lives in the database and reads back whole.
	dir := t.TempDir() + "/artifacts"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/plan.md", []byte("copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	got, err := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if err != nil || got.PlanMarkdown != goodSubmit().Plan || got.DecisionsMD != goodSubmit().Decisions || len(got.PRSplit) != 1 || len(got.ReviewFocus) != 1 {
		t.Fatalf("plan after retention = %+v, %v", got, err)
	}
}

// A sweep that sees the old planner ended while its replacement is starting
// must not fail the plan.
func TestPlan_SweepDuringRespawnKeepsPlanWriting(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.mustStartWithQuestions()
	e.sessions.ended[p.PlannerSessionID] = true
	e.sessions.duringSpawn = func() { e.plans.SessionEnded(e.ctx, p.PlannerSessionID) }
	if _, err := e.plans.Defaults(e.ctx, "p1", owner, e.item.Ref(), false); err != nil {
		t.Fatal(err)
	}
	cur, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if cur.Status != domain.PlanWriting || cur.PlannerSessionID == "" || cur.PlannerSessionID == p.PlannerSessionID {
		t.Fatalf("plan = %s planner %q; want writing with the new planner", cur.Status, cur.PlannerSessionID)
	}
	if _, err := e.plans.Submit(e.ctx, "p1", e.planner(cur), e.item.Ref(), goodSubmit()); err != nil {
		t.Fatalf("new planner refused: %v", err)
	}
}

func TestPlan_StatusChangesWriteChangeLog(t *testing.T) {
	e := newPlanEnv(t, true)
	before, _ := e.store.EventsAfter(e.ctx, 0, 10000)
	e.mustStartWithQuestions()
	after, err := e.store.EventsAfter(e.ctx, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	var states []string
	for _, ev := range after[len(before):] {
		if s := string(ev.Payload); strings.Contains(s, `"kind":"plan"`) {
			states = append(states, s)
		}
	}
	if len(states) < 2 || !strings.Contains(states[len(states)-1], "awaiting_answers") {
		t.Fatalf("plan events = %v; want drafting then awaiting_answers on the backlog stream", states)
	}
}

func TestPlan_StartRefusedWhenApproved(t *testing.T) {
	e := newPlanEnv(t, true)
	old := e.toApproval()
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.plans.Start(e.ctx, "p1", owner, e.item.Ref()); apiCode(t, err) != "PLAN_ALREADY_APPROVED" {
		t.Fatalf("Start over an approved plan: err = %v", err)
	}
	cur, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if cur.ID != old.ID || cur.Status != domain.PlanApproved {
		t.Fatalf("current plan = %s %s; the approved plan must stay current", cur.ID, cur.Status)
	}
}

func TestSpawnContext_PlanSectionIsCapped(t *testing.T) {
	e := newPlanEnv(t, true)
	e.toApproval()
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
	cur, _ := e.plans.Get(e.ctx, "p1", e.item.Ref())
	cur.PRSplit = nil
	for i := 0; i < 30; i++ {
		cur.PRSplit = append(cur.PRSplit, domain.PlanPR{Title: "PR", Files: []string{strings.Repeat("f/", 2000)}, Produces: []string{"x"}})
	}
	if got := backlog.PlanSpawnContextForTest(cur, "B1"); len([]rune(got)) > 16100 || !strings.Contains(got, "[truncated]") || !strings.Contains(got, "tallpine plan show B1") {
		t.Fatalf("context = %d runes", len([]rune(got)))
	}
}

func TestPlan_ReplanSpawnFailureKeepsApprovedPlan(t *testing.T) {
	e := newPlanEnv(t, true)
	e.toApproval()
	approved, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{})
	if err != nil {
		t.Fatal(err)
	}
	e.sessions.failSpawn = fmt.Errorf("no harness")
	if _, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref()); err == nil {
		t.Fatal("Replan succeeded with a failing spawn")
	}
	e.sessions.failSpawn = nil
	cur, err := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if err != nil || cur.ID != approved.ID || cur.Status != domain.PlanApproved {
		t.Fatalf("current plan = %+v, %v; the approved plan must stay in effect", cur, err)
	}
	item, err := e.bl.PrepareSpawn(e.ctx, "p1", e.item.Ref(), false)
	if err != nil || !strings.Contains(item.Context, "Approved plan") {
		t.Fatalf("implementer lost the decisions: %v %q", err, item.Context)
	}
}

func TestPlan_ReplanTerminatesSupersededPlanner(t *testing.T) {
	e := newPlanEnv(t, true)
	hung := e.mustStart() // an open plan whose planner hangs
	fresh, err := e.plans.Replan(e.ctx, "p1", owner, e.item.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if len(e.sessions.terminated) != 1 || e.sessions.terminated[0] != hung.PlannerSessionID || fresh.PlannerSessionID == hung.PlannerSessionID {
		t.Fatalf("terminated %v, want only the superseded planner %q", e.sessions.terminated, hung.PlannerSessionID)
	}
	// An approved plan's planner is stopped once its replacement has started.
	e2 := newPlanEnv(t, true)
	old := e2.toApproval()
	if _, err := e2.plans.Approve(e2.ctx, "p1", owner, e2.item.Ref(), backlog.PlanApproveInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.plans.Replan(e2.ctx, "p1", owner, e2.item.Ref()); err != nil {
		t.Fatal(err)
	}
	if len(e2.sessions.terminated) != 1 || e2.sessions.terminated[0] != old.PlannerSessionID {
		t.Fatalf("approved replan terminated %v, want %q", e2.sessions.terminated, old.PlannerSessionID)
	}
}

func TestPlan_StartAfterFailedWritingPassesTheAnswers(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.mustStartWithQuestions()
	if _, err := e.plans.Answer(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanAnswerInput{
		Questions: map[string]domain.PlanAnswer{"q1": {Other: "split by seat price"}}, Corrections: map[string]string{"s1": "15 minutes"}}); err != nil {
		t.Fatal(err)
	}
	e.plans.SessionEnded(e.ctx, p.PlannerSessionID) // planner ends while writing
	if cur, _ := e.plans.Get(e.ctx, "p1", e.item.Ref()); cur.Status != domain.PlanFailed {
		t.Fatalf("plan = %s, want failed", cur.Status)
	}
	if _, _, err := e.plans.Start(e.ctx, "p1", owner, e.item.Ref()); err != nil {
		t.Fatal(err)
	}
	last := e.sessions.spawned[len(e.sessions.spawned)-1].Prompt
	for _, want := range []string{"split by seat price", "15 minutes", "do not ask again"} {
		if !strings.Contains(last, want) {
			t.Errorf("new planner's brief lacks %q:\n%s", want, last)
		}
	}
}

func TestPlan_AlertsOwnerWhenWaiting(t *testing.T) {
	var got []string
	e := newPlanEnvWith(t, true, func(d *backlog.PlanDeps) {
		d.NotifyOwner = func(_ context.Context, p domain.BacklogPlan, ref, title string) {
			got = append(got, string(p.Status)+" "+ref+" "+title)
		}
	})
	e.toApproval()
	if len(got) != 2 || got[0][:len("awaiting_answers")] != "awaiting_answers" || got[1][:len("awaiting_approval")] != "awaiting_approval" {
		t.Fatalf("want one alert per waiting state, got %v", got)
	}
}
