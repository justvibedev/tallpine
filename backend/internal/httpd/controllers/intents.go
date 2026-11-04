package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/bodylimit"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

// IntentStore persists a project's durable orchestrator intents.
type IntentStore interface {
	InsertOrchestratorIntent(ctx context.Context, in domain.OrchestratorIntent) error
	SelectOrchestratorIntents(ctx context.Context, project domain.ProjectID, includeDone bool) ([]domain.OrchestratorIntent, error)
	MarkOrchestratorIntentDone(ctx context.Context, project domain.ProjectID, id string, at time.Time) (bool, error)
}

// IntentsController serves the durable orchestrator intents of a project.
type IntentsController struct{ Store IntentStore }

// IntentsProjectIDParam is the {projectId} path parameter of the intent routes.
type IntentsProjectIDParam struct {
	ProjectID string `path:"projectId" description:"Project whose orchestrator intents are read or written."`
}

// IntentIDParam addresses one intent.
type IntentIDParam struct {
	ProjectID string `path:"projectId" description:"Project that owns the intent."`
	IntentID  string `path:"intentId" description:"Intent to clear."`
}

// IntentsQuery filters the intent list.
type IntentsQuery struct {
	All bool `query:"all" description:"Include cleared intents."`
}

// CreateIntentRequest is the body of POST /projects/{projectId}/intents.
type CreateIntentRequest struct {
	Text string `json:"text"`
}

// IntentResponse is one intent.
type IntentResponse struct {
	ID        string     `json:"id"`
	Text      string     `json:"text"`
	CreatedBy string     `json:"createdBy"`
	CreatedAt time.Time  `json:"createdAt"`
	DoneAt    *time.Time `json:"doneAt,omitempty"`
}

// ListIntentsResponse is the body of GET /projects/{projectId}/intents.
type ListIntentsResponse struct {
	Intents []IntentResponse `json:"intents"`
}

// Register mounts the intent routes.
func (c *IntentsController) Register(r chi.Router) {
	r.Get("/projects/{projectId}/intents", c.list)
	r.Post("/projects/{projectId}/intents", c.create)
	r.Post("/projects/{projectId}/intents/{intentId}/done", c.done)
}

func intentResponse(in domain.OrchestratorIntent) IntentResponse {
	return IntentResponse{ID: in.ID, Text: in.Text, CreatedBy: in.CreatedBy, CreatedAt: in.CreatedAt, DoneAt: in.DoneAt}
}

func (c *IntentsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Store == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/projects/{projectId}/intents")
		return
	}
	items, err := c.Store.SelectOrchestratorIntents(r.Context(), domain.ProjectID(chi.URLParam(r, "projectId")), r.URL.Query().Get("all") == "true")
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := make([]IntentResponse, 0, len(items))
	for _, in := range items {
		out = append(out, intentResponse(in))
	}
	envelope.WriteJSON(w, http.StatusOK, ListIntentsResponse{Intents: out})
}

func (c *IntentsController) create(w http.ResponseWriter, r *http.Request) {
	if c.Store == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects/{projectId}/intents")
		return
	}
	bodylimit.Raise(w, r, 16<<10)
	var req CreateIntentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Expected a JSON body with text", nil)
		return
	}
	text, err := domain.NormalizeOrchestratorIntentText(req.Text)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INTENT_TEXT_INVALID", "Intent text must be 1 to 2048 bytes", nil)
		return
	}
	in := domain.OrchestratorIntent{
		ID: uuid.NewString(), ProjectID: domain.ProjectID(chi.URLParam(r, "projectId")), Text: text,
		CreatedBy: intentAuthor(r), CreatedAt: time.Now().UTC(),
	}
	if err := c.Store.InsertOrchestratorIntent(r.Context(), in); err != nil {
		if errors.Is(err, domain.ErrProjectUnknown) {
			envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PROJECT_NOT_FOUND", "Project not found", nil)
			return
		}
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, intentResponse(in))
}

func (c *IntentsController) done(w http.ResponseWriter, r *http.Request) {
	if c.Store == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects/{projectId}/intents/{intentId}/done")
		return
	}
	ok, err := c.Store.MarkOrchestratorIntentDone(r.Context(), domain.ProjectID(chi.URLParam(r, "projectId")), chi.URLParam(r, "intentId"), time.Now().UTC())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "INTENT_NOT_FOUND", "No open intent with that id", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// intentAuthor names who filed the intent: the agent session, else empty (the owner).
func intentAuthor(r *http.Request) string {
	if a, ok := requestscope.AgentFrom(r.Context()); ok {
		return string(a.SessionID)
	}
	return ""
}
