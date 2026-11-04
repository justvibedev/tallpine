package controllers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/accountadopt"
)

// AccountAdoptService is the HTTP boundary of "Use for all sessions".
type AccountAdoptService interface {
	Preview(ctx context.Context, accountID string) (accountadopt.Preview, error)
	Adopt(ctx context.Context, accountID string, policy domain.AccountAdoptionPolicy, key string) (batch domain.AccountAdoption, replay bool, err error)
	Get(ctx context.Context, accountID, batchID string) (domain.AccountAdoption, error)
	Latest(ctx context.Context, accountID string) (domain.AccountAdoption, error)
}

// AccountAdoptController switches every live session to one account.
type AccountAdoptController struct {
	Svc AccountAdoptService
}

// AgentAccountAdoptionIDParam documents the batch route identifiers.
type AgentAccountAdoptionIDParam struct {
	AccountID string `path:"accountId" description:"Agent account identifier."`
	BatchID   string `path:"batchId" description:"Account switch batch identifier."`
}

// AdoptAgentAccountRequest asks to use an account for every live session.
type AdoptAgentAccountRequest struct {
	Policy domain.AccountAdoptionPolicy `json:"policy" enum:"wait,interrupt" description:"wait lets a busy session finish its turn first; interrupt stops the turn."`
	// RequestKey makes a repeated request a replay: the same key returns the stored batch.
	RequestKey string `json:"requestKey" minLength:"1"`
}

// AccountAdoptionSession is a live session a switch would move.
type AccountAdoptionSession struct {
	SessionID      domain.SessionID `json:"sessionId"`
	ProjectID      domain.ProjectID `json:"projectId,omitempty"`
	Title          string           `json:"title"`
	IsOrchestrator bool             `json:"isOrchestrator"`
	Activity       string           `json:"activity"`
	// Running is false for a session whose agent has exited: it is only rebound.
	Running   bool   `json:"running"`
	AccountID string `json:"accountId"`
}

// AccountAdoptionPreviewResponse lists the sessions a switch would move.
type AccountAdoptionPreviewResponse struct {
	AccountID string                   `json:"accountId"`
	Sessions  []AccountAdoptionSession `json:"sessions"`
}

// AccountAdoptionItemResponse is one session's outcome.
type AccountAdoptionItemResponse struct {
	SessionID      domain.SessionID                `json:"sessionId"`
	IsOrchestrator bool                            `json:"isOrchestrator"`
	State          domain.AccountAdoptionItemState `json:"state" enum:"queued,moving,waiting,moved,rebound,failed,skipped"`
	ErrorCode      string                          `json:"errorCode,omitempty"`
	ErrorMessage   string                          `json:"errorMessage,omitempty"`
}

// AccountAdoptionResponse is a switch batch with its per-session outcomes.
type AccountAdoptionResponse struct {
	ID        string                        `json:"id"`
	AccountID string                        `json:"accountId"`
	Policy    domain.AccountAdoptionPolicy  `json:"policy" enum:"wait,interrupt"`
	State     domain.AccountAdoptionState   `json:"state" enum:"running,completed"`
	CreatedAt time.Time                     `json:"createdAt"`
	UpdatedAt time.Time                     `json:"updatedAt"`
	Items     []AccountAdoptionItemResponse `json:"items"`
}

// Register adds the adoption routes.
func (c *AccountAdoptController) Register(r chi.Router) {
	r.Get("/agents/accounts/{accountId}/adopt/preview", c.preview)
	r.Post("/agents/accounts/{accountId}/adopt", c.adopt)
	r.Get("/agents/accounts/{accountId}/adoptions/latest", c.latest)
	r.Get("/agents/accounts/{accountId}/adoptions/{batchId}", c.get)
}

func (c *AccountAdoptController) preview(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/agents/accounts/{accountId}/adopt/preview")
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "accountId"))
	p, err := c.Svc.Preview(r.Context(), id)
	if err != nil {
		writeAccountAdoptError(w, r, err)
		return
	}
	out := AccountAdoptionPreviewResponse{AccountID: p.Account.ID, Sessions: make([]AccountAdoptionSession, 0, len(p.Sessions))}
	for _, s := range p.Sessions {
		out.Sessions = append(out.Sessions, AccountAdoptionSession{
			SessionID: s.SessionID, ProjectID: s.ProjectID, Title: s.Title, IsOrchestrator: s.IsOrchestrator,
			Activity: s.Activity, Running: s.Running, AccountID: s.AccountID,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *AccountAdoptController) adopt(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/agents/accounts/{accountId}/adopt")
		return
	}
	var in AdoptAgentAccountRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "policy and requestKey are required", nil)
		return
	}
	batch, replay, err := c.Svc.Adopt(r.Context(), strings.TrimSpace(chi.URLParam(r, "accountId")), in.Policy, in.RequestKey)
	if err != nil {
		writeAccountAdoptError(w, r, err)
		return
	}
	status := http.StatusAccepted
	if replay {
		status = http.StatusOK
	}
	envelope.WriteJSON(w, status, accountAdoptionResponse(batch))
}

func (c *AccountAdoptController) latest(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/agents/accounts/{accountId}/adoptions/latest")
		return
	}
	batch, err := c.Svc.Latest(r.Context(), strings.TrimSpace(chi.URLParam(r, "accountId")))
	if err != nil {
		writeAccountAdoptError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, accountAdoptionResponse(batch))
}

func (c *AccountAdoptController) get(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/agents/accounts/{accountId}/adoptions/{batchId}")
		return
	}
	batch, err := c.Svc.Get(r.Context(), strings.TrimSpace(chi.URLParam(r, "accountId")), strings.TrimSpace(chi.URLParam(r, "batchId")))
	if err != nil {
		writeAccountAdoptError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, accountAdoptionResponse(batch))
}

func accountAdoptionResponse(b domain.AccountAdoption) AccountAdoptionResponse {
	out := AccountAdoptionResponse{
		ID: b.ID, AccountID: b.AccountID, Policy: b.Policy, State: b.State, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt,
		Items: make([]AccountAdoptionItemResponse, 0, len(b.Items)),
	}
	for _, it := range b.Items {
		out.Items = append(out.Items, AccountAdoptionItemResponse{
			SessionID: it.SessionID, IsOrchestrator: it.IsOrchestrator, State: it.State,
			ErrorCode: it.ErrorCode, ErrorMessage: it.ErrorMessage,
		})
	}
	return out
}

func writeAccountAdoptError(w http.ResponseWriter, r *http.Request, err error) {
	var busy *accountadopt.InProgressError
	switch {
	case errors.As(err, &busy):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "ADOPTION_IN_PROGRESS", busy.Error(), map[string]any{"batchId": busy.Batch.ID})
	case errors.Is(err, accountadopt.ErrNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "ADOPTION_NOT_FOUND", "Unknown account switch", nil)
	case errors.Is(err, accountadopt.ErrUnsupportedProvider):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "ADOPTION_PROVIDER_UNSUPPORTED", err.Error(), nil)
	case errors.Is(err, accountadopt.ErrInvalidPolicy), errors.Is(err, accountadopt.ErrRequestKeyRequired):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "ADOPTION_INVALID", err.Error(), nil)
	case errors.Is(err, ports.ErrAgentAccountSignedOut):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "AGENT_ACCOUNT_SIGNED_OUT", "This account is not signed in. Sign it in again, then retry.", nil)
	default:
		writeAgentAccountError(w, r, err)
	}
}
