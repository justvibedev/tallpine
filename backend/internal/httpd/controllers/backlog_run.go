package controllers

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
)

// BacklogRunService is the backlog run-mode surface the controller needs.
type BacklogRunService interface {
	Start(ctx context.Context, projectID domain.ProjectID, actor backlogsvc.Actor, in backlogsvc.RunStartInput) (backlogsvc.RunView, error)
	Get(ctx context.Context, projectID domain.ProjectID, ref string) (backlogsvc.RunView, error)
	List(ctx context.Context, projectID domain.ProjectID, openOnly bool, limit int) ([]domain.BacklogRun, error)
	SavePlan(ctx context.Context, projectID domain.ProjectID, actor backlogsvc.Actor, ref string, in backlogsvc.RunPlanInput) (backlogsvc.RunView, error)
	Pause(ctx context.Context, projectID domain.ProjectID, actor backlogsvc.Actor, ref string, in backlogsvc.RunPauseInput) (backlogsvc.RunView, error)
	Resume(ctx context.Context, projectID domain.ProjectID, actor backlogsvc.Actor, ref string) (backlogsvc.RunView, error)
	Stop(ctx context.Context, projectID domain.ProjectID, actor backlogsvc.Actor, ref string) (backlogsvc.RunView, error)
	SetItem(ctx context.Context, projectID domain.ProjectID, actor backlogsvc.Actor, ref, itemRef string, up backlogsvc.RunItemUpdate) (backlogsvc.RunView, error)
	Finish(ctx context.Context, projectID domain.ProjectID, actor backlogsvc.Actor, ref, summary string) (backlogsvc.RunView, error)
	ItemUsage(ctx context.Context, projectID domain.ProjectID, ref string) (backlogsvc.ItemUsage, error)
	Enabled() bool
}

// BacklogRunController owns the backlog run-mode routes. Every route answers
// 404 BACKLOG_RUN_DISABLED when TALLPINE_BACKLOG_RUN=off.
type BacklogRunController struct {
	Svc BacklogRunService
}

const runsBase = "/api/v1/projects/{projectId}/backlog-runs"

// Register mounts the run routes.
func (c *BacklogRunController) Register(r chi.Router) {
	r.Get("/projects/{projectId}/backlog-runs", c.list)
	r.Post("/projects/{projectId}/backlog-runs", c.start)
	r.Get("/projects/{projectId}/backlog-runs/{runRef}", c.get)
	r.Put("/projects/{projectId}/backlog-runs/{runRef}/plan", c.plan)
	r.Post("/projects/{projectId}/backlog-runs/{runRef}/pause", c.pause)
	r.Post("/projects/{projectId}/backlog-runs/{runRef}/resume", c.resume)
	r.Post("/projects/{projectId}/backlog-runs/{runRef}/stop", c.stop)
	r.Post("/projects/{projectId}/backlog-runs/{runRef}/finish", c.finish)
	r.Post("/projects/{projectId}/backlog-runs/{runRef}/items/{ref}", c.item)
	r.Get("/projects/{projectId}/backlog/{ref}/usage", c.usage)
}

// guard answers 501 when the service is not wired and returns the project.
func (c *BacklogRunController) guard(w http.ResponseWriter, r *http.Request, method, pattern string) (domain.ProjectID, bool) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, method, pattern)
		return "", false
	}
	if !c.Svc.Enabled() {
		// Before the body is read, so a switched-off daemon answers every call alike.
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "BACKLOG_RUN_DISABLED", "Backlog run mode is off (TALLPINE_BACKLOG_RUN=off on this daemon)", nil)
		return "", false
	}
	return domain.ProjectID(chi.URLParam(r, "projectId")), true
}

func runParam(r *http.Request, name string) string {
	v, err := url.PathUnescape(chi.URLParam(r, name))
	if err != nil {
		return chi.URLParam(r, name)
	}
	return v
}

func (c *BacklogRunController) writeRun(w http.ResponseWriter, r *http.Request, status int, v backlogsvc.RunView, err error) {
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, status, BacklogRunEnvelope{Run: backlogRunResponse(v)})
}

func (c *BacklogRunController) list(w http.ResponseWriter, r *http.Request) {
	project, ok := c.guard(w, r, "GET", runsBase)
	if !ok {
		return
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BACKLOG_FILTER", "limit must be a positive integer", nil)
			return
		}
		limit = n
	}
	runs, err := c.Svc.List(r.Context(), project, r.URL.Query().Get("open") == "1", limit)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := make([]BacklogRunResponse, 0, len(runs))
	for _, run := range runs {
		out = append(out, backlogRunResponse(backlogsvc.RunView{BacklogRun: run}))
	}
	envelope.WriteJSON(w, http.StatusOK, ListBacklogRunsResponse{Runs: out})
}

func (c *BacklogRunController) start(w http.ResponseWriter, r *http.Request) {
	project, ok := c.guard(w, r, "POST", runsBase)
	if !ok {
		return
	}
	var req StartBacklogRunRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	v, err := c.Svc.Start(r.Context(), project, backlogActor(r), backlogsvc.RunStartInput{
		Scope: domain.BacklogScope(req.Scope), Kind: domain.BacklogRunSelection(req.Selection), Refs: req.Refs,
	})
	c.writeRun(w, r, http.StatusCreated, v, err)
}

func (c *BacklogRunController) get(w http.ResponseWriter, r *http.Request) {
	project, ok := c.guard(w, r, "GET", runsBase+"/{runRef}")
	if !ok {
		return
	}
	v, err := c.Svc.Get(r.Context(), project, runParam(r, "runRef"))
	c.writeRun(w, r, http.StatusOK, v, err)
}

func (c *BacklogRunController) plan(w http.ResponseWriter, r *http.Request) {
	project, ok := c.guard(w, r, "PUT", runsBase+"/{runRef}/plan")
	if !ok {
		return
	}
	var req BacklogRunPlanRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	in := backlogsvc.RunPlanInput{Lanes: make([][]backlogsvc.RunPlanItem, 0, len(req.Lanes))}
	for _, lane := range req.Lanes {
		items := make([]backlogsvc.RunPlanItem, 0, len(lane))
		for _, it := range lane {
			items = append(items, backlogsvc.RunPlanItem{Ref: it.Ref, Model: it.Model})
		}
		in.Lanes = append(in.Lanes, items)
	}
	v, err := c.Svc.SavePlan(r.Context(), project, backlogActor(r), runParam(r, "runRef"), in)
	c.writeRun(w, r, http.StatusOK, v, err)
}

func (c *BacklogRunController) pause(w http.ResponseWriter, r *http.Request) {
	project, ok := c.guard(w, r, "POST", runsBase+"/{runRef}/pause")
	if !ok {
		return
	}
	var req BacklogRunPauseRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	v, err := c.Svc.Pause(r.Context(), project, backlogActor(r), runParam(r, "runRef"), backlogsvc.RunPauseInput{Reason: req.Reason, ResumeAt: req.ResumeAt})
	c.writeRun(w, r, http.StatusOK, v, err)
}

func (c *BacklogRunController) resume(w http.ResponseWriter, r *http.Request) {
	project, ok := c.guard(w, r, "POST", runsBase+"/{runRef}/resume")
	if !ok {
		return
	}
	v, err := c.Svc.Resume(r.Context(), project, backlogActor(r), runParam(r, "runRef"))
	c.writeRun(w, r, http.StatusOK, v, err)
}

func (c *BacklogRunController) stop(w http.ResponseWriter, r *http.Request) {
	project, ok := c.guard(w, r, "POST", runsBase+"/{runRef}/stop")
	if !ok {
		return
	}
	v, err := c.Svc.Stop(r.Context(), project, backlogActor(r), runParam(r, "runRef"))
	c.writeRun(w, r, http.StatusOK, v, err)
}

func (c *BacklogRunController) finish(w http.ResponseWriter, r *http.Request) {
	project, ok := c.guard(w, r, "POST", runsBase+"/{runRef}/finish")
	if !ok {
		return
	}
	var req BacklogRunFinishRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	v, err := c.Svc.Finish(r.Context(), project, backlogActor(r), runParam(r, "runRef"), req.Summary)
	c.writeRun(w, r, http.StatusOK, v, err)
}

func (c *BacklogRunController) item(w http.ResponseWriter, r *http.Request) {
	project, ok := c.guard(w, r, "POST", runsBase+"/{runRef}/items/{ref}")
	if !ok {
		return
	}
	var req BacklogRunItemRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	v, err := c.Svc.SetItem(r.Context(), project, backlogActor(r), runParam(r, "runRef"), runParam(r, "ref"),
		backlogsvc.RunItemUpdate{Status: domain.BacklogRunItemStatus(req.Status), Reason: req.Reason, Question: req.Question})
	c.writeRun(w, r, http.StatusOK, v, err)
}

func (c *BacklogRunController) usage(w http.ResponseWriter, r *http.Request) {
	project, ok := c.guard(w, r, "GET", "/api/v1/projects/{projectId}/backlog/{ref}/usage")
	if !ok {
		return
	}
	u, err := c.Svc.ItemUsage(r.Context(), project, runParam(r, "ref"))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, BacklogUsageEnvelope{Usage: backlogUsageResponse(u)})
}
