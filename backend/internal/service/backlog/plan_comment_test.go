package backlog_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func (s *planStore) UpdateBacklogPlan(ctx context.Context, id string, mutate func(*domain.BacklogPlan) error) (domain.BacklogPlan, error) {
	return s.Store.UpdateBacklogPlan(ctx, id, func(pl *domain.BacklogPlan) error {
		if err := mutate(pl); err != nil {
			return err
		}
		if s.failApproval != nil && pl.Status == domain.PlanApproved {
			return s.failApproval
		}
		return nil
	})
}

func (e *planEnv) current() domain.BacklogPlan {
	e.t.Helper()
	cur, err := e.plans.Get(e.ctx, "p1", e.item.Ref())
	if err != nil {
		e.t.Fatal(err)
	}
	return cur
}

func (e *planEnv) sentContains(text string) bool {
	for _, m := range e.sessions.sent {
		if strings.Contains(m, text) {
			return true
		}
	}
	return false
}

func (e *planEnv) notified(text string) bool {
	for _, m := range e.notify.sent {
		if strings.Contains(m, text) {
			return true
		}
	}
	return false
}

// B831: a comment on its own is stored with the plan, reaches the planner, and
// holds approval until the planner answers; accepting it leaves the plan to the owner.
func TestPlanComment_AloneReachesPlannerAndBlocksApproval(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.toApproval()
	got, err := e.plans.Comment(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanCommentInput{Text: "reuse the seat parser"})
	if err != nil || got.Status != domain.PlanAwaitingApproval || len(got.Comments) != 1 || !got.Comments[0].Pending() || got.Comments[0].By != "user" {
		t.Fatalf("Comment = %+v, %v", got.Comments, err)
	}
	if !e.sentContains("reuse the seat parser") || !e.sentContains("tallpine plan accept-comment "+e.item.Ref()) {
		t.Fatalf("planner was not told the comment and its options: %q", e.sessions.sent)
	}
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); apiCode(t, err) != "PLAN_COMMENT_PENDING" {
		t.Fatalf("approve while the comment is pending: err = %v", err)
	}
	accepted, err := e.plans.AcceptComments(e.ctx, "p1", e.planner(p), e.item.Ref())
	if err != nil || accepted.Status != domain.PlanAwaitingApproval || accepted.Comments[0].Outcome != domain.PlanCommentAccepted {
		t.Fatalf("AcceptComments = %s %+v, %v; want the plan back with the owner", accepted.Status, accepted.Comments, err)
	}
	if _, err := e.plans.AcceptComments(e.ctx, "p1", e.planner(p), e.item.Ref()); apiCode(t, err) != "PLAN_NO_COMMENT" {
		t.Fatalf("second accept: err = %v", err)
	}
	if got, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil || got.Status != domain.PlanApproved {
		t.Fatalf("approve after accept = %s, %v", got.Status, err)
	}
	// Only the planner answers a comment.
	if _, err := e.plans.Comment(e.ctx, "p1", backlog.Actor{Kind: domain.BacklogOriginWorker, SessionID: "w"}, e.item.Ref(), backlog.PlanCommentInput{Text: "x"}); apiCode(t, err) != "PLAN_ACTOR_FORBIDDEN" {
		t.Fatalf("worker comment: err = %v", err)
	}
}

// B831: Approve with a comment does not approve (no implementer) until the
// planner accepts the comment; then the approval lands as the owner's.
func TestPlanComment_ApproveWithCommentHeldUntilAccepted(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.toApproval()
	held, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{Comment: "fine, but keep the old API"})
	if err != nil || held.Status != domain.PlanAwaitingApproval || !held.Comments[0].Approve {
		t.Fatalf("approve with comment = %s %+v, %v; want held", held.Status, held.Comments, err)
	}
	if e.notified("is approved. Spawn") {
		t.Fatalf("orchestrator told to spawn before the planner accepted: %q", e.notify.sent)
	}
	if !e.sentContains("accepting it approves the plan") {
		t.Fatalf("planner not told the approval is held: %q", e.sessions.sent)
	}
	got, err := e.plans.AcceptComments(e.ctx, "p1", e.planner(p), e.item.Ref())
	if err != nil || got.Status != domain.PlanApproved || got.ApprovedBy != "user" || got.Comments[0].Outcome != domain.PlanCommentAccepted {
		t.Fatalf("accept = %s by %q %+v, %v; want approved by the owner", got.Status, got.ApprovedBy, got.Comments, err)
	}
	if !e.notified("is approved. Spawn") {
		t.Fatalf("no spawn notice after the held approval landed: %q", e.notify.sent)
	}
	ctx := backlog.PlanSpawnContextForTest(got, e.item.Ref())
	if !strings.Contains(ctx, "Owner comments on the plan") || !strings.Contains(ctx, "fine, but keep the old API (accepted by the planner") {
		t.Fatalf("implementer context lacks the comment:\n%s", ctx)
	}
}

// B831: an orchestrator relaying Approve with a comment needs the owner's
// message, checked when the comment is made; the landed approval keeps it.
func TestPlanComment_OrchestratorHeldApprovalKeepsOwnerMessage(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.toApproval()
	if _, err := e.plans.Approve(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{Comment: "ok with a tweak"}); apiCode(t, err) != "PLAN_OWNER_MESSAGE_REQUIRED" {
		t.Fatalf("no owner message: err = %v", err)
	}
	e.store.messages["m-ok"] = store.PlanMessageSource{Human: true, ProjectID: "p1", At: e.now.Add(time.Hour)}
	if _, err := e.plans.Approve(e.ctx, "p1", planOrch, e.item.Ref(), backlog.PlanApproveInput{Comment: "ok with a tweak", OwnerMessage: "m-ok"}); err != nil {
		t.Fatal(err)
	}
	got, err := e.plans.AcceptComments(e.ctx, "p1", e.planner(p), e.item.Ref())
	if err != nil || got.Status != domain.PlanApproved || got.ApprovedBy != "orchestrator:orch-1" || got.OwnerMessageID != "m-ok" {
		t.Fatalf("accept = %s by %q msg %q, %v", got.Status, got.ApprovedBy, got.OwnerMessageID, err)
	}
}

// B831: a revision answers the comment, drops the held approval, and asks the
// owner to approve the revised plan.
func TestPlanComment_RevisionResubmitsForApproval(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.toApproval()
	if _, err := e.plans.Submit(e.ctx, "p1", e.planner(p), e.item.Ref(), goodSubmit()); apiCode(t, err) != "PLAN_WRONG_STATE" {
		t.Fatalf("resubmit without a comment: err = %v", err)
	}
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{Comment: "split the backend PR"}); err != nil {
		t.Fatal(err)
	}
	rev := goodSubmit()
	rev.Plan = "# Plan\n\nRevised."
	got, err := e.plans.Submit(e.ctx, "p1", e.planner(p), e.item.Ref(), rev)
	if err != nil || got.Status != domain.PlanAwaitingApproval || got.PlanMarkdown != rev.Plan || got.Comments[0].Outcome != domain.PlanCommentRevised {
		t.Fatalf("revise = %s %q %+v, %v", got.Status, got.PlanMarkdown, got.Comments, err)
	}
	if _, ok := got.HeldApproval(); ok || e.notified("is approved. Spawn") {
		t.Fatal("the held approval carried over to the revised plan")
	}
	if got, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil || got.Status != domain.PlanApproved {
		t.Fatalf("approve revised = %s, %v", got.Status, err)
	}
}

// B831: the planner may answer a comment with follow-up questions on the card;
// without a pending comment a submitted plan takes no new questions.
func TestPlanComment_FollowUpQuestions(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.toApproval()
	if _, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), goodQuestions()); apiCode(t, err) != "PLAN_WRONG_STATE" {
		t.Fatalf("questions without a comment: err = %v", err)
	}
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{Comment: "what about refunds?"}); err != nil {
		t.Fatal(err)
	}
	follow := backlog.PlanQuestionsInput{Questions: []domain.PlanQuestion{question("r1")}}
	got, err := e.plans.SubmitQuestions(e.ctx, "p1", e.planner(p), e.item.Ref(), follow)
	if err != nil || got.Status != domain.PlanAwaitingAnswers || got.Answers != nil || got.Comments[0].Outcome != domain.PlanCommentQuestions {
		t.Fatalf("follow-up = %s answers %v %+v, %v", got.Status, got.Answers, got.Comments, err)
	}
	if got, err := e.plans.Defaults(e.ctx, "p1", owner, e.item.Ref(), false); err != nil || got.Status != domain.PlanWriting {
		t.Fatalf("answer follow-up = %s, %v", got.Status, err)
	}
	if e.notified("is approved. Spawn") {
		t.Fatal("follow-up questions let the held approval land")
	}
}

// B831: Replan with a comment hands it to the new planner as instructions and
// stores it on the new plan.
func TestPlanComment_ReplanCarriesInstructions(t *testing.T) {
	e := newPlanEnv(t, true)
	e.toApproval()
	fresh, err := e.plans.ReplanWithComment(e.ctx, "p1", owner, e.item.Ref(), "plan it as one PR")
	if err != nil || fresh.Status != domain.PlanDrafting {
		t.Fatalf("replan = %+v, %v", fresh, err)
	}
	prompt := e.sessions.spawned[len(e.sessions.spawned)-1].Prompt
	if !strings.Contains(prompt, "these instructions") || !strings.Contains(prompt, "> plan it as one PR") {
		t.Fatalf("new planner's brief lacks the instructions:\n%s", prompt)
	}
	if len(fresh.Comments) != 1 || !fresh.Comments[0].Replan || fresh.Comments[0].Outcome != domain.PlanCommentInstructions {
		t.Fatalf("new plan comments = %+v", fresh.Comments)
	}
	if cur := e.current(); len(cur.Comments) != 1 {
		t.Fatalf("stored comments = %+v", cur.Comments)
	}
}

// B831: a comment to an ended planner starts a new one with the comment in its
// brief; a planner that ends without answering releases the plan to the owner.
func TestPlanComment_EndedPlanner(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.toApproval()
	e.sessions.ended[p.PlannerSessionID] = true
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{Comment: "rename the flag"}); err != nil {
		t.Fatal(err)
	}
	cur := e.current()
	last := e.sessions.spawned[len(e.sessions.spawned)-1]
	if cur.PlannerSessionID == p.PlannerSessionID || !strings.Contains(last.Prompt, "rename the flag") || !strings.Contains(last.Prompt, "Who pays?") {
		t.Fatalf("planner %q (was %q); brief:\n%s", cur.PlannerSessionID, p.PlannerSessionID, last.Prompt)
	}
	e.plans.SessionEnded(e.ctx, cur.PlannerSessionID)
	cur = e.current()
	if cur.Status != domain.PlanAwaitingApproval || cur.Comments[0].Outcome != domain.PlanCommentUnanswered {
		t.Fatalf("after the planner ended: %s %+v", cur.Status, cur.Comments)
	}
	if e.notified("is approved. Spawn") {
		t.Fatal("the held approval landed without the planner accepting")
	}
	if got, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil || got.Status != domain.PlanApproved {
		t.Fatalf("owner approves again = %s, %v", got.Status, err)
	}
}

// B831 review: a held approval that fails to land after the planner accepted
// the comment is not lost silently: the owner and the orchestrator are told,
// and the owner can approve again.
func TestPlanComment_HeldApprovalFailureIsNotSilent(t *testing.T) {
	e := newPlanEnv(t, true)
	p := e.toApproval()
	if _, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{Comment: "ok if the flag stays"}); err != nil {
		t.Fatal(err)
	}
	e.store.failApproval = errors.New("disk full")
	got, err := e.plans.AcceptComments(e.ctx, "p1", e.planner(p), e.item.Ref())
	if err != nil || got.Status != domain.PlanAwaitingApproval || got.Comments[0].Outcome != domain.PlanCommentAccepted {
		t.Fatalf("accept = %s %+v, %v; want the accepted comment and the plan back with the owner", got.Status, got.Comments, err)
	}
	if !e.notified("could not land") || e.notified("is approved. Spawn") {
		t.Fatalf("orchestrator not told the held approval failed: %q", e.notify.sent)
	}
	e.store.failApproval = nil
	if got, err := e.plans.Approve(e.ctx, "p1", owner, e.item.Ref(), backlog.PlanApproveInput{}); err != nil || got.Status != domain.PlanApproved {
		t.Fatalf("owner approves again = %s, %v", got.Status, err)
	}
}
