package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	"tallpine.eu/tallpine/backend/internal/service/settingsaudit"
)

// AgentPolicyService is the controller-facing agent policy contract.
type AgentPolicyService interface {
	Describe(ctx context.Context, project string) agentpolicy.View
	Update(ctx context.Context, project string, update agentpolicy.Update, now time.Time) error
}

// AgentPolicyProjects resolves a project for the per-project routes (404 for an
// unknown one). projectsvc.Manager satisfies it.
type AgentPolicyProjects interface {
	Get(ctx context.Context, id domain.ProjectID) (projectsvc.GetResult, error)
}

// AgentPolicyDecisions reads the decision record a session launched under.
type AgentPolicyDecisions interface {
	SessionPolicyDecision(ctx context.Context, id domain.SessionID) (record []byte, updatedAt time.Time, ok bool, err error)
}

// SessionPolicyDecisionResponse is the agent policy decision a session's latest
// launch was made under: which rules and settings applied, the layer that set
// each, and what a seam changed (for example a lowered permission mode). It
// holds names and values of policy settings only, never a secret.
type SessionPolicyDecisionResponse struct {
	SessionID  string    `json:"sessionId"`
	RecordedAt time.Time `json:"recordedAt"`
	Record     any       `json:"record"`
}

// UpdateAgentPolicyRequest changes fields of one layer: rule -> field -> value.
// Omitted fields keep their stored value; at least one must be present. A field
// locked by managed config or the environment is refused with 409.
type UpdateAgentPolicyRequest struct {
	Rules map[string]map[string]any `json:"rules"`
}

// AgentPolicyController owns the agent policy routes.
type AgentPolicyController struct {
	// Audit records changes in the settings change log; nil records nothing.
	Audit     *settingsaudit.Recorder
	Svc       AgentPolicyService
	Projects  AgentPolicyProjects
	Decisions AgentPolicyDecisions
}

// Register mounts the agent policy routes.
func (c *AgentPolicyController) Register(r chi.Router) {
	r.Get("/settings/agent-policy", c.get)
	r.Patch("/settings/agent-policy", c.update)
	r.Get("/projects/{id}/agent-policy", c.get)
	r.Patch("/projects/{id}/agent-policy", c.update)
	r.Get("/sessions/{sessionId}/agent-policy", c.sessionDecision)
}

// scope returns the project of a per-project route ("" for the global one) and
// false when the project does not exist.
func (c *AgentPolicyController) scope(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if id == "" {
		return "", true
	}
	if c.Projects == nil {
		return id, true
	}
	if _, err := c.Projects.Get(r.Context(), domain.ProjectID(id)); err != nil {
		envelope.WriteError(w, r, err)
		return "", false
	}
	return id, true
}

func (c *AgentPolicyController) get(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/settings/agent-policy")
		return
	}
	project, ok := c.scope(w, r)
	if !ok {
		return
	}
	envelope.WriteJSON(w, http.StatusOK, agentPolicyResponse(c.Svc.Describe(r.Context(), project)))
}

func (c *AgentPolicyController) update(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/settings/agent-policy")
		return
	}
	project, ok := c.scope(w, r)
	if !ok {
		return
	}
	var req UpdateAgentPolicyRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if _, scoped := scopedAgent(r); scoped {
		if _, touches := req.Rules[agentpolicy.DevModeRule]; touches {
			writeAgentScopeForbidden(w, r, "change Dev mode: only the user may")
			return
		}
	}
	update := agentpolicy.Update{}
	for rule, fields := range req.Rules {
		update[rule] = map[string]json.RawMessage{}
		for field, value := range fields {
			encoded, err := json.Marshal(value)
			if err != nil {
				envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "AGENT_POLICY_INVALID", "invalid value for "+rule+"."+field, nil)
				return
			}
			update[rule][field] = encoded
		}
	}
	var locked *agentpolicy.LockedError
	var invalid *agentpolicy.ValidationError
	before := c.Svc.Describe(r.Context(), project)
	err := c.Svc.Update(r.Context(), project, update, time.Now())
	switch {
	case errors.As(err, &locked):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"POLICY_LOCKED", "This setting is managed by your organization and cannot be changed here", map[string]any{"fields": locked.Fields})
		return
	case errors.As(err, &invalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "AGENT_POLICY_INVALID", invalid.Message, nil)
		return
	case err != nil:
		envelope.WriteError(w, r, err)
		return
	}
	after := c.Svc.Describe(r.Context(), project)
	layer := agentpolicy.SourceUser
	if project != "" {
		layer = agentpolicy.SourceProject
	}
	c.Audit.Record(r.Context(), domain.ProjectID(project), settingsaudit.PolicyLayer(before, layer), settingsaudit.PolicyLayer(after, layer))
	envelope.WriteJSON(w, http.StatusOK, agentPolicyResponse(after))
}

func (c *AgentPolicyController) sessionDecision(w http.ResponseWriter, r *http.Request) {
	if c.Decisions == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/agent-policy")
		return
	}
	id := domain.SessionID(chi.URLParam(r, "sessionId"))
	raw, at, ok, err := c.Decisions.SessionPolicyDecision(r.Context(), id)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "POLICY_DECISION_NOT_FOUND", "No agent policy decision is recorded for this session", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionPolicyDecisionResponse{SessionID: string(id), RecordedAt: at, Record: decodeAny(raw)})
}

func decodeAny(raw json.RawMessage) any {
	var v any
	_ = json.Unmarshal(raw, &v)
	return v
}

func agentPolicyResponse(view agentpolicy.View) AgentPolicyResponse {
	out := AgentPolicyResponse{
		SchemaVersion: view.SchemaVersion, Project: view.Project, ManagedPath: view.ManagedPath, Profile: view.Profile,
		SecurityMode: AgentPolicySecurityMode{Enabled: view.SecurityMode.Enabled, Source: string(view.SecurityMode.Source), Locked: view.SecurityMode.Locked},
		Warnings:     view.Warnings, Rules: make([]AgentPolicyRule, 0, len(view.Rules)),
	}
	for _, rule := range view.Rules {
		rv := AgentPolicyRule{Name: rule.Name, TitleKey: rule.TitleKey, Fields: make([]AgentPolicyField, 0, len(rule.Fields))}
		for _, f := range rule.Fields {
			field := AgentPolicyField{
				Name: f.Name, Kind: string(f.Kind), Enum: f.Enum, LabelKey: f.LabelKey, DescriptionKey: f.DescriptionKey, OptionLabelKeys: f.OptionLabelKeys,
				Default: decodeAny(f.Default), Value: decodeAny(f.Value), Source: string(f.Source), Locked: f.Locked,
				ProjectOverride: string(f.ProjectOverride), Advanced: f.Advanced,
				OptionSource: string(f.OptionSource), SourceField: f.SourceField,
			}
			if f.VisibleWhen != nil {
				field.VisibleWhen = &AgentPolicyCondition{Field: f.VisibleWhen.Field, Equals: f.VisibleWhen.Equals}
			}
			rv.Fields = append(rv.Fields, field)
		}
		out.Rules = append(out.Rules, rv)
	}
	return out
}
