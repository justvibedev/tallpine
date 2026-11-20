package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxPlanCommentChars caps one owner comment on a plan (B831).
const MaxPlanCommentChars = 4000

// PlanCommentOutcome is what the planner made of an owner comment.
type PlanCommentOutcome string

// The comment outcomes. Pending waits on the planner.
const (
	PlanCommentPending      PlanCommentOutcome = ""             // the planner has not answered yet
	PlanCommentAccepted     PlanCommentOutcome = "accepted"     // taken as is, the plan stands
	PlanCommentRevised      PlanCommentOutcome = "revised"      // the planner resubmitted a revised plan
	PlanCommentQuestions    PlanCommentOutcome = "questions"    // the planner asked follow-up questions
	PlanCommentInstructions PlanCommentOutcome = "instructions" // given with a replan: the new planner's instructions
	PlanCommentUnanswered   PlanCommentOutcome = "unanswered"   // the planner ended or the plan moved on without an answer
)

// PlanComment is the owner's free-text comment on a whole plan, sent alone,
// with Approve, or with Replan. It is kept with the plan and reaches the
// implementer with the decisions table.
type PlanComment struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	// By is "user" or "orchestrator:<session id>".
	By string `json:"by"`
	// OwnerMessageID backs an orchestrator's approval held on this comment.
	OwnerMessageID string `json:"ownerMessageId,omitempty"`
	// Approve says the owner approved with this comment: the approval lands
	// only once the planner accepts the comment.
	Approve bool `json:"approve,omitempty"`
	// Replan says the comment came with Replan, as the new run's instructions.
	Replan     bool               `json:"replan,omitempty"`
	Outcome    PlanCommentOutcome `json:"outcome,omitempty"`
	At         time.Time          `json:"at"`
	ResolvedAt time.Time          `json:"resolvedAt,omitzero"`
}

// Pending reports whether the planner still has to answer the comment.
func (c PlanComment) Pending() bool { return c.Outcome == PlanCommentPending }

// ValidatePlanComment checks one comment's text.
func ValidatePlanComment(text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("the comment is empty")
	}
	if utf8.RuneCountInString(text) > MaxPlanCommentChars {
		return fmt.Errorf("the comment is over %d characters", MaxPlanCommentChars)
	}
	return nil
}

// PendingComments returns the comments the planner has not answered.
func (p BacklogPlan) PendingComments() []PlanComment {
	var out []PlanComment
	for _, c := range p.Comments {
		if c.Pending() {
			out = append(out, c)
		}
	}
	return out
}

// HeldApproval returns the pending comment the owner approved with, if any:
// the plan is approved once the planner accepts it.
func (p BacklogPlan) HeldApproval() (PlanComment, bool) {
	for _, c := range p.Comments {
		if c.Pending() && c.Approve {
			return c, true
		}
	}
	return PlanComment{}, false
}

// ResolveComments stamps every pending comment with outcome and returns how
// many it resolved.
func (p *BacklogPlan) ResolveComments(outcome PlanCommentOutcome, at time.Time) int {
	n := 0
	for i := range p.Comments {
		if p.Comments[i].Pending() {
			p.Comments[i].Outcome, p.Comments[i].ResolvedAt = outcome, at
			n++
		}
	}
	return n
}
