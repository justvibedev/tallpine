package httpd

// The cross-project half of the agent scope gate (crossProjectOrchestration):
// which projects an orchestrator created, which of them are marked eval, and
// the checks that let it register, configure, start and drive them.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/crossprojectorchestration"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

// evalChecker is requestscope.EvalChecker over the scope gate's creator and
// eval-mark lookups: a project qualifies only if the caller's project created
// it AND it is marked eval.
type evalChecker struct {
	scopes  *agentScopes
	creator domain.ProjectID
}

func (c evalChecker) EvalProject(ctx context.Context, project domain.ProjectID) bool {
	return c.scopes.evalMarked != nil && c.scopes.createdBy(ctx, project, c.creator) && c.scopes.evalMarked(ctx, project)
}

// projectConfigStore is the part of the daemon store that reads a project's
// config (ProjectConfig.Eval).
type projectConfigStore interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// projectEvalMarked reads the eval mark from the store behind the scope gate;
// a store without project reads, or a failed read, means not marked.
func projectEvalMarked(store AgentScopeStore) func(context.Context, domain.ProjectID) bool {
	reader, ok := store.(projectConfigStore)
	if !ok {
		return nil
	}
	return func(ctx context.Context, project domain.ProjectID) bool {
		rec, found, err := reader.GetProject(ctx, string(project))
		return err == nil && found && rec.Config.Eval
	}
}

// createdBy reports whether project was registered by an orchestrator of the
// creator project.
func (s *agentScopes) createdBy(ctx context.Context, project, creator domain.ProjectID) bool {
	if s.creators == nil || project == "" || creator == "" {
		return false
	}
	got, ok, err := s.creators.ProjectCreator(ctx, project)
	return err == nil && ok && got == creator
}

// ownConfigReason is the refusal for an orchestrator's own-project config PUT:
// B189 keeps those writes on the rules route.
const ownConfigReason = "an orchestrator may set its own project's agentRules/orchestratorRules text through PUT /projects/{id}/rules (tallpine project set-config --agent-rules / --orchestrator-rules); agentRulesFile and other config fields are the owner's to set"

// checkCreatedProject lets an opted-in orchestrator act on a project its own
// project created, and on no other.
func (s *agentScopes) checkCreatedProject(r *http.Request, project domain.ProjectID, agent requestscope.Agent) (reason, cross string) {
	if agent.Role != requestscope.RoleOrchestrator {
		return "it targets another project", ""
	}
	if !agent.CrossProject {
		return "it targets another project: " + crossOffReason, ""
	}
	if !s.createdBy(r.Context(), project, agent.ProjectID) {
		return "it targets a project this project did not create", ""
	}
	if r.Method == http.MethodPut {
		raw, ok := peekBody(r)
		if !ok {
			return "its body is unreadable or too large", ""
		}
		if why := configBodyViolation(raw); why != "" {
			return why, ""
		}
	}
	return "", "created_project_access"
}

// peekBody reads the request body once to apply scope and puts it back for the
// handler. ok is false when the body is too large or unreadable.
func peekBody(r *http.Request) ([]byte, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxScopedBody+1))
	if err != nil || len(raw) > maxScopedBody {
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return raw, true
}

// checkNewProject lets an opted-in orchestrator register a project at a local
// path. Workspace projects (which init git and commit) and prepared clones stay
// with the user.
func (s *agentScopes) checkNewProject(r *http.Request, agent requestscope.Agent) (reason, cross string) {
	if agent.Role != requestscope.RoleOrchestrator || !agent.CrossProject {
		return "it creates a project: " + crossOffReason, ""
	}
	raw, ok := peekBody(r)
	if !ok {
		return "its body is unreadable or too large", ""
	}
	var body struct {
		AsWorkspace        bool            `json:"asWorkspace"`
		ClonePreparationID string          `json:"clonePreparationId"`
		Config             json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		// Let the handler report the malformed body.
		return "", "project_create"
	}
	if body.AsWorkspace || body.ClonePreparationID != "" {
		return "agents may register single-repository projects only", ""
	}
	if why := agentConfigViolation(body.Config); why != "" {
		return why, ""
	}
	return "", "project_create"
}

// checkStartOrchestrator lets an opted-in orchestrator start the plain
// orchestrator of a project its own project created. A lane start goes through
// checkStartLane (policy orchestratorLanes); clean replacement and approval
// overrides stay with the user.
func (s *agentScopes) checkStartOrchestrator(r *http.Request, agent requestscope.Agent) (reason, cross string) {
	if raw, ok := peekBody(r); ok {
		var lane struct {
			Lane string `json:"lane"`
		}
		if json.Unmarshal(raw, &lane) == nil && lane.Lane != "" {
			return s.checkStartLane(r, agent, raw)
		}
	}
	if agent.Role != requestscope.RoleOrchestrator || !agent.CrossProject {
		return "it starts an orchestrator: " + crossOffReason, ""
	}
	raw, ok := peekBody(r)
	if !ok {
		return "its body is unreadable or too large", ""
	}
	var body struct {
		ProjectID    domain.ProjectID `json:"projectId"`
		Clean        bool             `json:"clean"`
		Lane         string           `json:"lane"`
		ApprovalMode string           `json:"approvalMode"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.ProjectID == "" {
		return "its body does not name a project", ""
	}
	if !s.createdBy(r.Context(), body.ProjectID, agent.ProjectID) {
		return "it starts an orchestrator in a project this project did not create", ""
	}
	if body.Clean || body.Lane != "" || body.ApprovalMode != "" {
		return "agents may start only the default orchestrator, without clean, lane or approvalMode", ""
	}
	return "", "orchestrator_start"
}

// crossProjectEnabled reads the crossProjectOrchestration policy of a project
// from the live engine on every request. No engine means off.
func crossProjectEnabled(decider controllers.PolicyDecider) func(context.Context, domain.ProjectID) bool {
	if decider == nil {
		return nil
	}
	return func(ctx context.Context, project domain.ProjectID) bool {
		return crossprojectorchestration.Of(decider.Decide(ctx, agentpolicy.SessionRef{Project: project})).Enabled()
	}
}

// auditCross records an allowed request that reaches outside the agent's own
// project, next to the refusals audit writes.
func (s *agentScopes) auditCross(r *http.Request, agent requestscope.Agent, action string) {
	s.log.Warn("daemon allowed a cross-project agent request",
		"audit", "agent_scope", "outcome", "cross_project_allowed", "scope", "cross_project", "action", action,
		"method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr,
		"role", string(agent.Role), "session", string(agent.SessionID), "project", string(agent.ProjectID))
}
