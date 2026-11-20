package controllers_test

import (
	"testing"
)

// submittedPlan drives B1's plan to awaiting_approval and returns the planner's agent header.
func submittedPlan(t *testing.T, a *backlogAPI, base string) string {
	t.Helper()
	a.must(201, "POST", base, "", map[string]any{"title": "first", "open": true})
	plan := a.must(201, "POST", base+"/B1/plan", "", nil)["plan"].(map[string]any)
	planner := "worker:" + plan["plannerSessionId"].(string)
	a.must(200, "POST", base+"/B1/plan/questions", planner, map[string]any{"questions": []any{}, "assumptions": []any{}})
	sub := map[string]any{"plan": "# Plan", "decisions": "| q | a |", "prs": []any{map[string]any{"title": "PR1", "produces": []any{"X()"}}}}
	a.must(200, "POST", base+"/B1/plan/submit", planner, sub)
	return planner
}

// B831: Approve with a comment holds the approval until the planner accepts
// the comment; the plan view carries the comment and its outcome.
func TestPlanAPI_ApproveWithCommentHeldUntilPlannerAccepts(t *testing.T) {
	a := newPlanAPI(t, true)
	base := "/projects/p1/backlog"
	planner := submittedPlan(t, a, base)
	held := a.must(200, "POST", base+"/B1/plan/approve", "", map[string]any{"comment": "keep the old flag"})["plan"].(map[string]any)
	comments := held["comments"].([]any)
	if held["status"] != "awaiting_approval" || len(comments) != 1 {
		t.Fatalf("approve with comment = %v", held)
	}
	if c := comments[0].(map[string]any); c["outcome"] != "pending" || c["approve"] != true || c["text"] != "keep the old flag" {
		t.Fatalf("comment = %v", c)
	}
	a.must(409, "POST", base+"/B1/plan/approve", "", map[string]any{})
	a.must(403, "POST", base+"/B1/plan/accept-comment", "worker:other", nil)
	done := a.must(200, "POST", base+"/B1/plan/accept-comment", planner, nil)["plan"].(map[string]any)
	if done["status"] != "approved" || done["approvedBy"] != "user" || done["comments"].([]any)[0].(map[string]any)["outcome"] != "accepted" {
		t.Fatalf("after accept = %v", done)
	}
}

// B831: a comment on its own, and Replan with a comment that reaches the new plan.
func TestPlanAPI_CommentAloneAndReplanComment(t *testing.T) {
	a := newPlanAPI(t, true)
	base := "/projects/p1/backlog"
	submittedPlan(t, a, base)
	a.must(400, "POST", base+"/B1/plan/comment", "", map[string]any{"text": "  "})
	got := a.must(200, "POST", base+"/B1/plan/comment", "", map[string]any{"text": "why two PRs?"})["plan"].(map[string]any)
	if got["status"] != "awaiting_approval" || len(got["comments"].([]any)) != 1 {
		t.Fatalf("comment = %v", got)
	}
	fresh := a.must(201, "POST", base+"/B1/plan/replan", "", map[string]any{"comment": "one PR please"})["plan"].(map[string]any)
	c := fresh["comments"].([]any)
	if fresh["status"] != "drafting" || len(c) != 1 || c[0].(map[string]any)["outcome"] != "instructions" || c[0].(map[string]any)["replan"] != true {
		t.Fatalf("replan with comment = %v", fresh)
	}
}
