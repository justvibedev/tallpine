package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
)

// BacklogService is the controller-facing backlog contract.
type BacklogService interface {
	Create(ctx context.Context, projectID domain.ProjectID, actor backlogsvc.Actor, in backlogsvc.CreateInput) (backlogsvc.View, error)
	Get(ctx context.Context, projectID domain.ProjectID, idOrRef string) (backlogsvc.View, error)
	List(ctx context.Context, projectID domain.ProjectID, f backlogsvc.ListFilter) ([]backlogsvc.View, error)
	Update(ctx context.Context, projectID domain.ProjectID, idOrRef string, in backlogsvc.UpdateInput) (backlogsvc.View, error)
	SetField(ctx context.Context, projectID domain.ProjectID, idOrRef, key, value string) (backlogsvc.View, error)
	SetState(ctx context.Context, projectID domain.ProjectID, idOrRef string, state domain.BacklogState, reason string) (backlogsvc.View, error)
	Move(ctx context.Context, projectID domain.ProjectID, in backlogsvc.MoveInput) error
	Delete(ctx context.Context, projectID domain.ProjectID, idOrRef string) (backlogsvc.DeleteResult, error)
	Purge(ctx context.Context, projectID domain.ProjectID, in backlogsvc.PurgeInput) (backlogsvc.DeleteResult, error)
	Link(ctx context.Context, projectID domain.ProjectID, idOrRef string, kind domain.BacklogLinkKind, ref, createdBy string) (backlogsvc.View, error)
	Unlink(ctx context.Context, projectID domain.ProjectID, idOrRef string, kind domain.BacklogLinkKind, ref string) (backlogsvc.View, error)
	Next(ctx context.Context, projectID domain.ProjectID, parentRef string) (backlogsvc.View, bool, error)
	InboxCounts(ctx context.Context) (map[domain.ProjectID]int, error)
}

// BacklogController owns the project backlog routes. Every route is
// project-scoped, so agent scope is a single own-project check.
type BacklogController struct {
	Svc BacklogService
}

const backlogBodyLimit = 256 << 10

// Register mounts the backlog routes.
func (c *BacklogController) Register(r chi.Router) {
	r.Get("/backlog/inbox-counts", c.inboxCounts)
	r.Get("/projects/{projectId}/backlog", c.list)
	r.Post("/projects/{projectId}/backlog", c.create)
	r.Get("/projects/{projectId}/backlog/next", c.next)
	r.Post("/projects/{projectId}/backlog/reorder", c.reorder)
	r.Post("/projects/{projectId}/backlog/purge", c.purge)
	r.Get("/projects/{projectId}/backlog/{ref}", c.get)
	r.Delete("/projects/{projectId}/backlog/{ref}", c.remove)
	r.Patch("/projects/{projectId}/backlog/{ref}", c.update)
	r.Post("/projects/{projectId}/backlog/{ref}/state", c.setState)
	r.Post("/projects/{projectId}/backlog/{ref}/links", c.link)
	r.Delete("/projects/{projectId}/backlog/{ref}/links", c.unlink)
	r.Put("/projects/{projectId}/backlog/{ref}/fields/{key}", c.setField)
	r.Delete("/projects/{projectId}/backlog/{ref}/fields/{key}", c.clearField)
}

func backlogActor(r *http.Request) backlogsvc.Actor {
	agent, ok := scopedAgent(r)
	if !ok {
		return backlogsvc.Actor{Kind: domain.BacklogOriginHuman}
	}
	kind := domain.BacklogOriginWorker
	switch agent.Role {
	case requestscope.RoleOrchestrator:
		kind = domain.BacklogOriginOrchestrator
	case requestscope.RoleReviewer:
		kind = domain.BacklogOriginReviewer
	}
	return backlogsvc.Actor{Kind: kind, SessionID: string(agent.SessionID)}
}

// backlogRoute extracts the project and (optional) item ref from the path,
// answering 400 on a malformed escape.
func (c *BacklogController) backlogRoute(w http.ResponseWriter, r *http.Request, method, pattern string) (domain.ProjectID, string, bool) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, method, pattern)
		return "", "", false
	}
	ref, err := url.PathUnescape(chi.URLParam(r, "ref"))
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "BACKLOG_REF_INVALID", "Invalid item ref", nil)
		return "", "", false
	}
	return domain.ProjectID(chi.URLParam(r, "projectId")), ref, true
}

func decodeBacklogBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, backlogBodyLimit))
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil {
		if _, more := dec.Token(); !errors.Is(more, io.EOF) {
			err = errors.New("trailing data")
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			envelope.WriteAPIError(w, r, http.StatusRequestEntityTooLarge, "bad_request", "BACKLOG_BODY_TOO_LARGE", "Backlog request body is too large", nil)
		} else {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		}
		return false
	}
	return true
}

func (c *BacklogController) list(w http.ResponseWriter, r *http.Request) {
	project, _, ok := c.backlogRoute(w, r, "GET", "/api/v1/projects/{projectId}/backlog")
	if !ok {
		return
	}
	q := r.URL.Query()
	limit := 0
	if v := q.Get("limit"); v != "" {
		n, err := parsePositiveInt(v)
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BACKLOG_FILTER", "limit must be a positive integer", nil)
			return
		}
		limit = n
	}
	items, err := c.Svc.List(r.Context(), project, backlogsvc.ListFilter{
		State: domain.BacklogState(q.Get("state")), ParentRef: q.Get("parent"), Held: q.Get("held"),
		Query: q.Get("q"), Limit: limit,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := make([]BacklogItemResponse, 0, len(items))
	for _, it := range items {
		out = append(out, backlogItemResponse(it))
	}
	envelope.WriteJSON(w, http.StatusOK, ListBacklogResponse{Items: out})
}

func (c *BacklogController) create(w http.ResponseWriter, r *http.Request) {
	project, _, ok := c.backlogRoute(w, r, "POST", "/api/v1/projects/{projectId}/backlog")
	if !ok {
		return
	}
	var req CreateBacklogRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	v, err := c.Svc.Create(r.Context(), project, backlogActor(r), backlogsvc.CreateInput{
		Title: req.Title, Note: req.Note, DoneWhen: req.DoneWhen, Scope: domain.BacklogScope(req.Scope),
		Hold: req.Hold, ParentRef: req.Parent, AgentNotes: req.AgentNotes, Open: req.Open, Excerpt: req.Excerpt,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, BacklogItemEnvelope{Item: backlogItemResponse(v)})
}

func (c *BacklogController) get(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.backlogRoute(w, r, "GET", "/api/v1/projects/{projectId}/backlog/{ref}")
	if !ok {
		return
	}
	v, err := c.Svc.Get(r.Context(), project, ref)
	c.writeItem(w, r, v, err)
}

func (c *BacklogController) next(w http.ResponseWriter, r *http.Request) {
	project, _, ok := c.backlogRoute(w, r, "GET", "/api/v1/projects/{projectId}/backlog/next")
	if !ok {
		return
	}
	v, found, err := c.Svc.Next(r.Context(), project, r.URL.Query().Get("parent"))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := NextBacklogResponse{}
	if found {
		item := backlogItemResponse(v)
		resp.Item = &item
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *BacklogController) update(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.backlogRoute(w, r, "PATCH", "/api/v1/projects/{projectId}/backlog/{ref}")
	if !ok {
		return
	}
	var req UpdateBacklogRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	in := backlogsvc.UpdateInput{
		Title: req.Title, Note: req.Note, DoneWhen: req.DoneWhen, Hold: req.Hold, AgentNotes: req.AgentNotes,
		ParentRef: req.Parent,
	}
	if req.Scope != nil {
		scope := domain.BacklogScope(*req.Scope)
		in.Scope = &scope
	}
	if req.Parent != nil && *req.Parent == "" {
		in.ParentRef, in.ClearParent = nil, true
	}
	v, err := c.Svc.Update(r.Context(), project, ref, in)
	c.writeItem(w, r, v, err)
}

func (c *BacklogController) setState(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.backlogRoute(w, r, "POST", "/api/v1/projects/{projectId}/backlog/{ref}/state")
	if !ok {
		return
	}
	var req BacklogStateRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	v, err := c.Svc.SetState(r.Context(), project, ref, domain.BacklogState(req.State), req.Reason)
	c.writeItem(w, r, v, err)
}

func (c *BacklogController) reorder(w http.ResponseWriter, r *http.Request) {
	project, _, ok := c.backlogRoute(w, r, "POST", "/api/v1/projects/{projectId}/backlog/reorder")
	if !ok {
		return
	}
	var req BacklogReorderRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	if err := c.Svc.Move(r.Context(), project, backlogsvc.MoveInput{Ref: req.Ref, BeforeRef: req.Before, AfterRef: req.After}); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *BacklogController) remove(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.backlogRoute(w, r, "DELETE", "/api/v1/projects/{projectId}/backlog/{ref}")
	if !ok {
		return
	}
	res, err := c.Svc.Delete(r.Context(), project, ref)
	c.writeDeleted(w, r, res, err)
}

func (c *BacklogController) purge(w http.ResponseWriter, r *http.Request) {
	project, _, ok := c.backlogRoute(w, r, "POST", "/api/v1/projects/{projectId}/backlog/purge")
	if !ok {
		return
	}
	var req BacklogPurgeRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	res, err := c.Svc.Purge(r.Context(), project, backlogsvc.PurgeInput{States: req.States, OlderThanDays: req.OlderThanDays, DryRun: req.DryRun})
	c.writeDeleted(w, r, res, err)
}

func (c *BacklogController) writeDeleted(w http.ResponseWriter, r *http.Request, res backlogsvc.DeleteResult, err error) {
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, BacklogDeleteResponse{Deleted: res.Deleted, Skipped: res.Skipped})
}

func (c *BacklogController) link(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.backlogRoute(w, r, "POST", "/api/v1/projects/{projectId}/backlog/{ref}/links")
	if !ok {
		return
	}
	var req BacklogLinkRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	v, err := c.Svc.Link(r.Context(), project, ref, domain.BacklogLinkKind(req.Kind), req.Ref, backlogActor(r).SessionID)
	c.writeItem(w, r, v, err)
}

func (c *BacklogController) unlink(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.backlogRoute(w, r, "DELETE", "/api/v1/projects/{projectId}/backlog/{ref}/links")
	if !ok {
		return
	}
	var req BacklogLinkRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	v, err := c.Svc.Unlink(r.Context(), project, ref, domain.BacklogLinkKind(req.Kind), req.Ref)
	c.writeItem(w, r, v, err)
}

func (c *BacklogController) setField(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.backlogRoute(w, r, "PUT", "/api/v1/projects/{projectId}/backlog/{ref}/fields/{key}")
	if !ok {
		return
	}
	var req BacklogFieldRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	v, err := c.Svc.SetField(r.Context(), project, ref, chi.URLParam(r, "key"), req.Value)
	c.writeItem(w, r, v, err)
}

func (c *BacklogController) clearField(w http.ResponseWriter, r *http.Request) {
	project, ref, ok := c.backlogRoute(w, r, "DELETE", "/api/v1/projects/{projectId}/backlog/{ref}/fields/{key}")
	if !ok {
		return
	}
	v, err := c.Svc.SetField(r.Context(), project, ref, chi.URLParam(r, "key"), "")
	c.writeItem(w, r, v, err)
}

func (c *BacklogController) writeItem(w http.ResponseWriter, r *http.Request, v backlogsvc.View, err error) {
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, BacklogItemEnvelope{Item: backlogItemResponse(v)})
}

func backlogItemResponse(v backlogsvc.View) BacklogItemResponse {
	links := make([]BacklogLinkResponse, 0, len(v.LinkViews))
	for _, l := range v.LinkViews {
		links = append(links, BacklogLinkResponse{Kind: string(l.Kind), Ref: l.Ref, Display: l.Display, CreatedBy: l.CreatedBy, CreatedAt: l.CreatedAt})
	}
	counts := map[string]int{}
	for k, n := range v.LinkCounts {
		counts[string(k)] = n
	}
	fields := v.Fields
	if fields == nil {
		fields = map[string]string{}
	}
	var plan *BacklogItemPlan
	if v.Plan != nil {
		plan = &BacklogItemPlan{ID: v.Plan.ID, Status: string(v.Plan.Status), Stale: v.Plan.Stale, ItemChanged: v.Plan.ItemChanged}
	}
	return BacklogItemResponse{
		Plan: plan,
		ID:   v.ID, Ref: v.Ref(), ProjectID: string(v.ProjectID), Title: v.Title, Note: v.Note, DoneWhen: v.DoneWhen,
		State: string(v.State), Hold: v.Hold, Scope: string(v.Scope), ParentID: v.ParentID, ParentRef: v.ParentRef,
		ChildCount: v.ChildCount, ChildClosedCount: v.ChildClosedCount, Position: v.Position,
		Origin:     BacklogOriginResponse{Kind: string(v.Origin.Kind), SessionID: v.Origin.SessionID, Excerpt: v.Origin.Excerpt},
		AgentNotes: v.AgentNotes, Fields: fields, ClosedReason: v.ClosedReason,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, ClosedAt: v.ClosedAt, Links: links, LinkCounts: counts,
		AutoClose: v.AutoClose, OpenBlockers: append([]string{}, v.OpenBlockers...),
		Badges: BacklogBadgesResponse{Active: v.Badges.Active, InReview: v.Badges.InReview, Landed: v.Badges.Landed},
	}
}

func parsePositiveInt(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, errors.New("not a positive integer")
	}
	return n, nil
}
