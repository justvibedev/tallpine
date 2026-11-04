package controllers

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

func (c *SessionsController) spawnOrchestrator(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/orchestrators")
		return
	}
	var in SpawnOrchestratorRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if in.ProjectID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROJECT_ID_REQUIRED", "projectId is required", nil)
		return
	}
	if in.Mode != "" {
		if _, err := domain.ParseSessionMode(string(in.Mode)); err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "SESSION_MODE_INVALID", err.Error(), nil)
			return
		}
	}
	if !in.ApprovalMode.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_APPROVAL_MODE", "approvalMode is invalid", nil)
		return
	}
	if len(in.Prompt) > maxPromptLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROMPT_TOO_LONG", "Prompt must be 16 KiB or fewer", nil)
		return
	}
	laneItemID := ""
	if in.Lane != "" {
		if c.Backlog == nil {
			envelope.WriteAPIError(w, r, http.StatusNotImplemented, "not_implemented", "LANE_UNAVAILABLE", "lanes need the backlog service", nil)
			return
		}
		id, closed, err := c.Backlog.ResolveLaneItem(r.Context(), in.ProjectID, in.Lane)
		if err != nil {
			envelope.WriteError(w, r, err)
			return
		}
		if closed != "" {
			// A closed or dropped lane item cannot start a new orchestrator,
			// but a still-running one stays reachable until its lane is ended.
			running, found, err := c.Svc.FindLaneOrchestrator(r.Context(), in.ProjectID, id)
			if err != nil {
				envelope.WriteError(w, r, err)
				return
			}
			if found && !in.Clean {
				envelope.WriteJSON(w, http.StatusOK, SpawnOrchestratorResponse{
					Orchestrator: OrchestratorResponse{ID: running.ID, ProjectID: running.ProjectID},
				})
				return
			}
			if _, err := c.Backlog.ResolveLane(r.Context(), in.ProjectID, in.Lane); err != nil {
				envelope.WriteError(w, r, err)
				return
			}
		}
		laneItemID = id
	}
	if strings.TrimSpace(in.Prompt) == "" {
		sess, err := c.Svc.SpawnLaneOrchestrator(r.Context(), in.ProjectID, in.Clean, in.Mode, in.ApprovalMode, laneItemID)
		if err != nil {
			envelope.WriteError(w, r, err)
			return
		}
		envelope.WriteJSON(w, http.StatusCreated, SpawnOrchestratorResponse{
			Orchestrator: OrchestratorResponse{ID: sess.ID, ProjectID: sess.ProjectID},
		})
		return
	}
	// The prompt is the new orchestrator's task brief: it rides the spawn, which
	// delivers it once the chat controller is live, as an unframed first user
	// turn, like a worker's spawn prompt. An agent caller got here only through
	// the creator check (agent_scope checkStartOrchestrator), so the brief comes
	// from the orchestrator whose project created this one.
	spawnCtx := r.Context()
	if agent, ok := scopedAgent(r); ok && laneItemID != "" {
		spawnCtx = sessionsvc.WithAgentLane(spawnCtx, sessionsvc.AgentLaneRequest{StartedBy: agent.SessionID, Model: strings.TrimSpace(in.Model)})
	} else if laneItemID != "" && strings.TrimSpace(in.Model) != "" {
		spawnCtx = sessionsvc.WithAgentLane(spawnCtx, sessionsvc.AgentLaneRequest{Model: strings.TrimSpace(in.Model)})
	}
	sess, created, err := c.Svc.SpawnLaneOrchestratorBrief(spawnCtx, in.ProjectID, in.Clean, in.Mode, in.ApprovalMode, laneItemID, in.Prompt)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if agent, ok := scopedAgent(r); ok && created {
		slog.Warn("trusted initial brief delivered to a created project's orchestrator",
			"audit", "agent_scope", "outcome", "orchestrator_brief_delivered",
			"creator_session", string(agent.SessionID), "creator_project", string(agent.ProjectID),
			"session", string(sess.ID), "project", string(sess.ProjectID), "bytes", len(in.Prompt))
	}
	status := http.StatusCreated
	envelope.WriteJSON(w, status, SpawnOrchestratorResponse{
		Orchestrator:    OrchestratorResponse{ID: sess.ID, ProjectID: sess.ProjectID},
		PromptDelivered: created,
	})
}

func (c *SessionsController) endLane(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/orchestrators/end")
		return
	}
	var in EndLaneRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	id := in.SessionID
	if id == "" {
		if in.ProjectID == "" || in.Lane == "" {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "END_LANE_TARGET_REQUIRED", "sessionId, or projectId and lane, is required", nil)
			return
		}
		if c.Backlog == nil {
			envelope.WriteAPIError(w, r, http.StatusNotImplemented, "not_implemented", "LANE_UNAVAILABLE", "lanes need the backlog service", nil)
			return
		}
		laneID, _, err := c.Backlog.ResolveLaneItem(r.Context(), in.ProjectID, in.Lane)
		if err != nil {
			envelope.WriteError(w, r, err)
			return
		}
		orch, found, err := c.Svc.FindLaneOrchestrator(r.Context(), in.ProjectID, laneID)
		if err != nil {
			envelope.WriteError(w, r, err)
			return
		}
		if !found {
			envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "LANE_ORCHESTRATOR_NOT_FOUND", "No orchestrator is running for lane "+in.Lane, nil)
			return
		}
		id = orch.ID
	}
	endCtx := r.Context()
	if _, ok := scopedAgent(r); ok {
		endCtx = sessionsvc.WithAgentLane(endCtx, sessionsvc.AgentLaneRequest{RefuseOpenPR: true})
	}
	out, err := c.Svc.EndLane(endCtx, id, in.DryRun)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if out.Workers == nil {
		out.Workers = []domain.SessionID{}
	}
	if out.NotifyFailed == nil {
		out.NotifyFailed = []domain.SessionID{}
	}
	envelope.WriteJSON(w, http.StatusOK, EndLaneResponse(out))
}

func (c *SessionsController) handoffOrchestrator(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/orchestrators/{id}/handoff")
		return
	}
	var in HandoffOrchestratorRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &in); err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
			return
		}
	}
	// The route rule already limits an agent token to its own session; this
	// keeps the controller safe if a rule is ever loosened.
	if agent, ok := scopedAgent(r); ok && (agent.Role != requestscope.RoleOrchestrator || agent.SessionID != orchestratorID(r)) {
		writeAgentScopeForbidden(w, r, "hand off an orchestrator other than its own session")
		return
	}
	sess, err := c.Svc.HandoffOrchestrator(r.Context(), orchestratorID(r), in.NotePath)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, SpawnOrchestratorResponse{
		Orchestrator: OrchestratorResponse{ID: sess.ID, ProjectID: sess.ProjectID},
	})
}

func (c *SessionsController) listOrchestrators(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/orchestrators")
		return
	}
	sessions, err := c.Svc.List(r.Context(), sessionsvc.ListFilter{OrchestratorOnly: true})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ListSessionsResponse{Sessions: c.views(r.Context(), r, visibleToAgent(r, sessions))})
}

func (c *SessionsController) getOrchestrator(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/orchestrators/{id}")
		return
	}
	sess, err := c.Svc.Get(r.Context(), orchestratorID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if sess.Kind != domain.KindOrchestrator {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", "Unknown session", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: c.view(r.Context(), r, sess)})
}

func orchestratorID(r *http.Request) domain.SessionID {
	return domain.SessionID(chi.URLParam(r, "id"))
}
