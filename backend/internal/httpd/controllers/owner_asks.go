package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	asksvc "tallpine.eu/tallpine/backend/internal/service/ask"
)

// OwnerAskService is the ask surface the controller needs (B698).
type OwnerAskService interface {
	Create(ctx context.Context, project domain.ProjectID, actor asksvc.Actor, in asksvc.CreateInput) (domain.OwnerAsk, error)
	List(ctx context.Context, project domain.ProjectID, openOnly bool) ([]domain.OwnerAsk, error)
	Answer(ctx context.Context, project domain.ProjectID, actor asksvc.Actor, ref string, in asksvc.AnswerInput) (domain.OwnerAsk, bool, error)
	Withdraw(ctx context.Context, project domain.ProjectID, actor asksvc.Actor, ref, reason string) (domain.OwnerAsk, error)
}

// OwnerAskController serves the tracked owner asks of a project.
type OwnerAskController struct {
	Svc OwnerAskService
}

const asksBase = "/api/v1/projects/{projectId}/asks"

// Register mounts the ask routes.
func (c *OwnerAskController) Register(r chi.Router) {
	r.Get("/projects/{projectId}/asks", c.list)
	r.Post("/projects/{projectId}/asks", c.create)
	r.Post("/projects/{projectId}/asks/{ref}/answer", c.answer)
	r.Post("/projects/{projectId}/asks/{ref}/withdraw", c.withdraw)
}

func askActor(r *http.Request) asksvc.Actor {
	a := backlogActor(r)
	return asksvc.Actor{Kind: a.Kind, SessionID: a.SessionID}
}

func (c *OwnerAskController) guard(w http.ResponseWriter, r *http.Request, method, pattern string) bool {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, method, pattern)
		return false
	}
	return true
}

func (c *OwnerAskController) write(w http.ResponseWriter, r *http.Request, status int, a domain.OwnerAsk, err error) {
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, status, OwnerAskEnvelope{Ask: ownerAskView(a)})
}

func (c *OwnerAskController) list(w http.ResponseWriter, r *http.Request) {
	if !c.guard(w, r, "GET", asksBase) {
		return
	}
	asks, err := c.Svc.List(r.Context(), domain.ProjectID(chi.URLParam(r, "projectId")), r.URL.Query().Get("state") != "all")
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := OwnerAsksResponse{Asks: []OwnerAskView{}}
	for _, a := range asks {
		out.Asks = append(out.Asks, ownerAskView(a))
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *OwnerAskController) create(w http.ResponseWriter, r *http.Request) {
	if !c.guard(w, r, "POST", asksBase) {
		return
	}
	var req OwnerAskCreateRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	in := asksvc.CreateInput{Text: req.Text, Reason: req.Reason, About: req.About, Recommended: req.Recommended, Reask: req.Reask}
	for _, o := range req.Options {
		in.Options = append(in.Options, domain.PlanOption{ID: o.ID, Label: o.Label})
	}
	a, err := c.Svc.Create(r.Context(), domain.ProjectID(chi.URLParam(r, "projectId")), askActor(r), in)
	c.write(w, r, http.StatusCreated, a, err)
}

func (c *OwnerAskController) answer(w http.ResponseWriter, r *http.Request) {
	if !c.guard(w, r, "POST", asksBase+"/{ref}/answer") {
		return
	}
	var req OwnerAskAnswerRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	a, failed, err := c.Svc.Answer(r.Context(), domain.ProjectID(chi.URLParam(r, "projectId")), askActor(r), runParam(r, "ref"),
		asksvc.AnswerInput{Option: req.Option, Other: req.Other, OwnerMessage: req.OwnerMessage})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, OwnerAskEnvelope{Ask: ownerAskView(a), DeliveryFailed: failed})
}

func (c *OwnerAskController) withdraw(w http.ResponseWriter, r *http.Request) {
	if !c.guard(w, r, "POST", asksBase+"/{ref}/withdraw") {
		return
	}
	var req OwnerAskWithdrawRequest
	if !decodeBacklogBody(w, r, &req) {
		return
	}
	a, err := c.Svc.Withdraw(r.Context(), domain.ProjectID(chi.URLParam(r, "projectId")), askActor(r), runParam(r, "ref"), req.Reason)
	c.write(w, r, http.StatusOK, a, err)
}
