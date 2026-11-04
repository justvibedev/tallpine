package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
)

// BacklogPlanService is the plan surface the controller needs (B205).
type BacklogPlanService interface {
	Enabled() bool
	Stale(ctx context.Context, plan domain.BacklogPlan) bool
	Get(ctx context.Context, projectID domain.ProjectID, ref string) (domain.BacklogPlan, error)
	ListOpen(ctx context.Context, projectID domain.ProjectID) ([]backlogsvc.ProjectPlan, error)
	Start(ctx context.Context, projectID domain.ProjectID, actor backlogsvc.Actor, ref string) (domain.BacklogPlan, bool, error)
	Replan(ctx context.Context, projectID domain.ProjectID, actor backlogsvc.Actor, ref string) (domain.BacklogPlan, error)
	SubmitQuestions(ctx context.Context, projectID domain.ProjectID, actor backlogsvc.Actor, ref string, in backlogsvc.PlanQuestionsInput) (domain.BacklogPlan, error)
	Submit(ctx context.Context, projectID domain.ProjectID, actor backlogsvc.Actor, ref string, in backlogsvc.PlanSubmitInput) (domain.BacklogPlan, error)
	Answer(ctx context.Context, projectID domain.ProjectID, actor backlogsvc.Actor, ref string, in backlogsvc.PlanAnswerInput) (domain.BacklogPlan, error)
	Defaults(ctx context.Context, projectID domain.ProjectID, actor backlogsvc.Actor, ref string, relayed bool) (domain.BacklogPlan, error)
	Approve(ctx context.Context, projectID domain.ProjectID, actor backlogsvc.Actor, ref string, in backlogsvc.PlanApproveInput) (domain.BacklogPlan, error)
	BuildAnyway(ctx context.Context, projectID domain.ProjectID, actor backlogsvc.Actor, ref string, in backlogsvc.PlanApproveInput) (domain.BacklogPlan, error)
}

// BacklogPlanController owns the plan routes of a backlog item. Every route
// answers 404 PLANNER_DISABLED while TALLPINE_PLANNER is off.
type BacklogPlanController struct {
	Svc BacklogPlanService
	// UsageLimits, when set, lets the plan say its planner waits for a usage-limit reset.
	UsageLimits UsageLimitReader
}

// view renders a plan and, while its planner is drafting or writing, the
// reset time it is paused until, so a client shows the wait instead of work.
func (c *BacklogPlanController) view(ctx context.Context, p domain.BacklogPlan) PlanView {
	v := planView(p)
	if c.UsageLimits == nil || p.PlannerSessionID == "" ||
		(p.Status != domain.PlanDrafting && p.Status != domain.PlanWriting) {
		return v
	}
	if until, ok := c.UsageLimits.UsageLimitPausedUntil(ctx, p.PlannerSessionID); ok {
		u := until.UTC()
		v.PausedUntil = &u
	}
	return v
}

const planBase = "/api/v1/projects/{projectId}/backlog/{ref}/plan"

// Register mounts the plan routes.
func (c *BacklogPlanController) Register(r chi.Router) {
	r.Get("/projects/{projectId}/plans", c.list)
	r.Get("/projects/{projectId}/backlog/{ref}/plan", c.get)
	r.Post("/projects/{projectId}/backlog/{ref}/plan", c.start)
	r.Post("/projects/{projectId}/backlog/{ref}/plan/replan", c.replan)
	r.Post("/projects/{projectId}/backlog/{ref}/plan/questions", c.questions)
	r.Post("/projects/{projectId}/backlog/{ref}/plan/submit", c.submit)
	r.Post("/projects/{projectId}/backlog/{ref}/plan/answers", c.answers)
	r.Post("/projects/{projectId}/backlog/{ref}/plan/defaults", c.defaults)
	r.Post("/projects/{projectId}/backlog/{ref}/plan/approve", c.approve)
	r.Post("/projects/{projectId}/backlog/{ref}/plan/build-anyway", c.buildAnyway)
}

// guard answers 501 when unwired and 404 while the switch is off, before any
// body is read, and returns the project and item ref.
func (c *BacklogPlanController) guard(w http.ResponseWriter, r *http.Request, method, pattern string) (domain.ProjectID, string, bool) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, method, pattern)
		return "", "", false
	}
	if !c.Svc.Enabled() {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PLANNER_DISABLED", "The planner is off (TALLPINE_PLANNER on this daemon)", nil)
		return "", "", false
	}
	return domain.ProjectID(chi.URLParam(r, "projectId")), runParam(r, "ref"), true
}

func (c *BacklogPlanController) writePlan(w http.ResponseWriter, r *http.Request, status int, p domain.BacklogPlan, err error) {
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, status, PlanEnvelope{Plan: c.view(r.Context(), p)})
}

func (c *BacklogPlanController) list(w http.ResponseWriter, r *http.Request) {
	project, _, ok := c.guard(w, r, "GET", "/api/v1/projects/{projectId}/plans")
	if !ok {
		return
	}
	plans, err := c.Svc.ListOpen(r.Context(), project)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := ProjectPlansResponse{Plans: []ProjectPlanDTO{}}
	for _, p := range plans {
		out.Plans = append(out.Plans, ProjectPlanDTO{ItemRef: p.ItemRef, ItemTitle: p.ItemTitle, Plan: c.view(r.Context(), p.Plan)})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *BacklogPlanController) get(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.guard(w, r, "GET", planBase)
	if !ok {
		return
	}
	p, err := c.Svc.Get(r.Context(), project, ref)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	v := c.view(r.Context(), p)
	v.Stale = c.Svc.Stale(r.Context(), p)
	envelope.WriteJSON(w, http.StatusOK, PlanEnvelope{Plan: v})
}

func (c *BacklogPlanController) start(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.guard(w, r, "POST", planBase)
	if !ok {
		return
	}
	p, created, err := c.Svc.Start(r.Context(), project, backlogActor(r), ref)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.writePlan(w, r, status, p, err)
}

func (c *BacklogPlanController) replan(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.guard(w, r, "POST", planBase+"/replan")
	if !ok {
		return
	}
	p, err := c.Svc.Replan(r.Context(), project, backlogActor(r), ref)
	c.writePlan(w, r, http.StatusCreated, p, err)
}

func (c *BacklogPlanController) questions(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.guard(w, r, "POST", planBase+"/questions")
	if !ok {
		return
	}
	var req PlanQuestionsRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	qs, as := req.toInput()
	p, err := c.Svc.SubmitQuestions(r.Context(), project, backlogActor(r), ref, backlogsvc.PlanQuestionsInput{Questions: qs, Assumptions: as})
	c.writePlan(w, r, http.StatusOK, p, err)
}

func (c *BacklogPlanController) submit(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.guard(w, r, "POST", planBase+"/submit")
	if !ok {
		return
	}
	var req PlanSubmitRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	in := backlogsvc.PlanSubmitInput{Plan: req.Plan, Decisions: req.Decisions, Focus: req.Focus}
	for _, pr := range req.PRs {
		in.PRs = append(in.PRs, domain.PlanPR(pr))
	}
	p, err := c.Svc.Submit(r.Context(), project, backlogActor(r), ref, in)
	c.writePlan(w, r, http.StatusOK, p, err)
}

func (c *BacklogPlanController) answers(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.guard(w, r, "POST", planBase+"/answers")
	if !ok {
		return
	}
	var req PlanAnswersRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	in := backlogsvc.PlanAnswerInput{Questions: map[string]domain.PlanAnswer{}, Corrections: req.Corrections, Relayed: req.Relayed}
	for id, a := range req.Questions {
		in.Questions[id] = domain.PlanAnswer(a)
	}
	if in.Corrections == nil {
		in.Corrections = map[string]string{}
	}
	p, err := c.Svc.Answer(r.Context(), project, backlogActor(r), ref, in)
	c.writePlan(w, r, http.StatusOK, p, err)
}

func (c *BacklogPlanController) defaults(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.guard(w, r, "POST", planBase+"/defaults")
	if !ok {
		return
	}
	var req PlanDefaultsRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	p, err := c.Svc.Defaults(r.Context(), project, backlogActor(r), ref, req.Relayed)
	c.writePlan(w, r, http.StatusOK, p, err)
}

func (c *BacklogPlanController) approve(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.guard(w, r, "POST", planBase+"/approve")
	if !ok {
		return
	}
	var req PlanApproveRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	p, err := c.Svc.Approve(r.Context(), project, backlogActor(r), ref, backlogsvc.PlanApproveInput{OwnerMessage: req.OwnerMessage})
	c.writePlan(w, r, http.StatusOK, p, err)
}

func (c *BacklogPlanController) buildAnyway(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.guard(w, r, "POST", planBase+"/build-anyway")
	if !ok {
		return
	}
	var req PlanApproveRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	p, err := c.Svc.BuildAnyway(r.Context(), project, backlogActor(r), ref, backlogsvc.PlanApproveInput{OwnerMessage: req.OwnerMessage})
	c.writePlan(w, r, http.StatusOK, p, err)
}
