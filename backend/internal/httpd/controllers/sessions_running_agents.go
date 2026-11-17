package controllers

import (
	"context"
	"net/http"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

// runningAgentsService is optional so focused SessionService fakes need not
// implement it; production's session service does.
type runningAgentsService interface {
	RunningAgents(ctx context.Context) ([]domain.SessionID, error)
	StopAllAgents(ctx context.Context) (sessionsvc.StopAllAgentsOutcome, error)
}

var _ runningAgentsService = (*sessionsvc.Service)(nil)

// runningAgents lists the sessions whose agent is still running. The desktop
// reads it before quitting so it can say how many keep running.
func (c *SessionsController) runningAgents(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(runningAgentsService)
	if !ok {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/running-agents")
		return
	}
	ids, err := svc.RunningAgents(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, RunningAgentsResponse{SessionIDs: nonNilIDs(ids)})
}

// stopAllAgents stops every running agent's process tree, keeping each
// session. The desktop's quit dialog and tray use it.
func (c *SessionsController) stopAllAgents(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(runningAgentsService)
	if !ok {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/running-agents/stop")
		return
	}
	out, err := svc.StopAllAgents(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	failures := out.Failures
	if failures == nil {
		failures = []string{}
	}
	envelope.WriteJSON(w, http.StatusOK, StopAllAgentsResponse{
		OK:       len(out.Failures) == 0,
		Stopped:  nonNilIDs(out.Stopped),
		Failures: failures,
	})
}

func nonNilIDs(ids []domain.SessionID) []domain.SessionID {
	if ids == nil {
		return []domain.SessionID{}
	}
	return ids
}
