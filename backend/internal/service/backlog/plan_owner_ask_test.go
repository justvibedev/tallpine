package backlog_test

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
)

// answerAsk stores an answered ask: about is its item key, at when it was answered.
func (e *planEnv) answerAsk(text, about string, relayed bool, at time.Time) {
	e.answerAskOpt(text, about, "go", relayed, at)
}

func (e *planEnv) answerAskOpt(text, about, option string, relayed bool, at time.Time) {
	e.t.Helper()
	a, err := e.store.CreateOwnerAsk(e.ctx, domain.OwnerAsk{ID: "ask-" + text, ProjectID: "p1", Text: text, About: about, State: domain.AskOpen, CreatedAt: at.Add(-time.Minute), UpdatedAt: at},
		func([]domain.OwnerAsk, *domain.OwnerAsk) error { return nil })
	if err != nil {
		e.t.Fatal(err)
	}
	a.State, a.Answer, a.AnsweredAt = domain.AskAnswered, &domain.AskAnswer{OptionID: option, Text: text, Relayed: relayed, MessageID: "m-" + text}, at
	if err := e.store.SaveOwnerAskTransition(e.ctx, a); err != nil {
		e.t.Fatal(err)
	}
}

// An ask the owner answered themselves (a "Build anyway" tap) after the plan
// changed backs an orchestrator's approval; old, relayed or other-item asks do not.
func TestPlan_OrchestratorApprovesOnAnsweredAsk(t *testing.T) {
	e := newPlanEnv(t, true)
	e.toApproval()
	approve := func(id string) (domain.BacklogPlan, error) {
		return e.plans.Approve(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{OwnerMessage: id})
	}
	ref, later := e.item.Ref(), e.now.Add(time.Hour)
	if _, err := approve("last:go"); apiCode(t, err) != "PLAN_OWNER_MESSAGE_STALE" {
		t.Fatalf("no ask: %v", err)
	}
	e.answerAsk("old", ref, false, e.now.Add(-time.Hour))           // Q1
	e.answerAsk("relayed", ref, true, later)                        // Q2: relayed, even with a message
	e.answerAsk("elsewhere", "B999", false, later.Add(time.Minute)) // Q3: another item
	if _, err := approve("last:go"); apiCode(t, err) != "PLAN_OWNER_MESSAGE_STALE" {
		t.Fatalf("old, relayed or other-item ask via last: %v", err)
	}
	for id, want := range map[string]string{"Q1": "PLAN_OWNER_ASK_STALE", "Q2": "PLAN_OWNER_ASK_RELAYED", "Q3": "PLAN_OWNER_ASK_OTHER_ITEM", "Q9": "PLAN_OWNER_MESSAGE_INVALID"} {
		if _, err := approve(id + ":go"); apiCode(t, err) != want { // each refusal says why
			t.Fatalf("%s: %v, want %s", id, err, want)
		}
	}
	e.answerAsk("Build anyway", ref, false, later.Add(2*time.Minute)) // Q4
	got, err := approve("last:go")
	if err != nil || got.OwnerMessageID != "ask:Q4" {
		t.Fatalf("answered ask: %q, %v", got.OwnerMessageID, err)
	}
}

// An explicit ref, in any case, names the owner-answered ask.
func TestPlan_ApproveOnAnswerRefAnyCase(t *testing.T) {
	e := newPlanEnv(t, true)
	e.toApproval()
	e.answerAsk("tap", "", false, e.now.Add(time.Hour))
	got, err := e.plans.Approve(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{OwnerMessage: "q1:go"})
	if err != nil || got.OwnerMessageID != "ask:Q1" {
		t.Fatalf("q1: %q, %v", got.OwnerMessageID, err)
	}
}

// build-anyway rests on the same owner proof as approve.
func TestPlan_BuildAnywayOnAnsweredAsk(t *testing.T) {
	e := newPlanEnv(t, true)
	e.approved(goodSubmit())
	build := func(id string) error {
		_, err := e.plans.BuildAnyway(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{OwnerMessage: id})
		return err
	}
	later := time.Now().UTC().Add(time.Hour)
	e.answerAsk("plain", e.item.Ref(), true, later)
	if err := build("last:go"); apiCode(t, err) != "PLAN_OWNER_MESSAGE_STALE" {
		t.Fatalf("relayed answer: %v", err)
	}
	e.answerAsk("tap", e.item.Ref(), false, later)
	if err := build("last:go"); err != nil {
		t.Fatalf("owner-answered ask: %v", err)
	}
}

// Only the approving option backs the call: another option or free text does not.
func TestPlan_AnsweredAskNeedsApprovingOption(t *testing.T) {
	e := newPlanEnv(t, true)
	e.toApproval()
	ref, at := e.item.Ref(), e.now.Add(time.Hour)
	e.answerAskOpt("replan", ref, "replan", false, at) // Q1: the owner picked another option
	approve := func(id string) error {
		_, err := e.plans.Approve(e.ctx, "p1", planOrch, ref, backlog.PlanApproveInput{OwnerMessage: id})
		return err
	}
	for id, want := range map[string]string{
		"Q1:go": "PLAN_OWNER_ASK_OPTION_MISMATCH", "last:go": "PLAN_OWNER_ASK_OPTION_MISMATCH",
		"Q1": "PLAN_OWNER_ASK_OPTION_REQUIRED", "last": "PLAN_OWNER_ASK_OPTION_REQUIRED",
	} {
		if err := approve(id); apiCode(t, err) != want {
			t.Errorf("%s: %v, want %s", id, err, want)
		}
	}
}
