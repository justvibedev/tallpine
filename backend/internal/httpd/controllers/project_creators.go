package controllers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/crossprojectorchestration"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

// ProjectCreatorStore remembers which project's orchestrator registered a
// project (agent policy crossProjectOrchestration).
type ProjectCreatorStore interface {
	SetProjectCreator(ctx context.Context, project, creator domain.ProjectID, at time.Time) error
	// ProjectCreator reports ok=false for a project a person registered.
	ProjectCreator(ctx context.Context, project domain.ProjectID) (creator domain.ProjectID, ok bool, err error)
}

// listForAgent narrows the project list for a scoped agent: its own project,
// plus (for an opted-in orchestrator) the id, name and kind of the others. The
// others' paths and errors are not shown.
func listForAgent(agent requestscope.Agent, projects []projectsvc.Summary) []projectsvc.Summary {
	out := projects[:0:0]
	for _, project := range projects {
		switch {
		case project.ID == agent.ProjectID || agent.MayActOn(project.ID):
			out = append(out, project)
		case agent.CrossProject && agent.Role == requestscope.RoleOrchestrator:
			out = append(out, projectsvc.Summary{ID: project.ID, Name: project.Name, Kind: project.Kind})
		}
	}
	return out
}

// afterAgentCreatedProject runs after an orchestrator registered a project
// (Service.Add already recorded the creator): it turns
// crossProjectOrchestration on for the new project so its orchestrator can
// answer. A locked policy (managed off) leaves the new project's setting off.
func (c *ProjectsController) afterAgentCreatedProject(r *http.Request, agent requestscope.Agent, project domain.ProjectID) {
	if c.Policy != nil {
		update := agentpolicy.Update{crossprojectorchestration.Name: {crossprojectorchestration.FieldEnabled: json.RawMessage(`true`)}}
		if err := c.Policy.Update(r.Context(), string(project), update, time.Now()); err != nil {
			slog.Warn("enable cross-project orchestration on created project", "project", project, "err", err)
		}
	}
	slog.Warn("project created by an orchestrator", "audit", "agent_scope", "outcome", "project_created",
		"project", project, "creator", agent.ProjectID, "session", agent.SessionID)
}

var permissionRank = map[domain.PermissionMode]int{
	domain.PermissionModeDefault:           0,
	domain.PermissionModeAcceptEdits:       1,
	domain.PermissionModeAuto:              2,
	domain.PermissionModeBypassPermissions: 3,
}

// permissionCapViolation says why cfg gives a role a permission mode more
// permissive than the creator project's own setting for that role ("" if none).
// The creator's limit is the mode a session of that role would launch with
// (session_manager.EffectivePermissions); an unknown mode ranks above all.
func permissionCapViolation(creator, cfg domain.ProjectConfig) string {
	limit := func(kind domain.SessionKind) int {
		return permissionRank[sessionmanager.EffectivePermissions(kind, creator)]
	}
	exceeds := func(set domain.PermissionMode, kind domain.SessionKind) bool {
		if set == "" {
			return false
		}
		rank, ok := permissionRank[set]
		return !ok || rank > limit(kind)
	}
	if exceeds(cfg.Worker.AgentConfig.Permissions, domain.KindWorker) {
		return "worker permissions more permissive than this project's own"
	}
	if exceeds(cfg.Orchestrator.AgentConfig.Permissions, domain.KindOrchestrator) {
		return "orchestrator permissions more permissive than this project's own"
	}
	// Reviewer launchers use the worker-role mode, so cap them by it.
	for _, reviewer := range cfg.Reviewers {
		if exceeds(reviewer.AgentConfig.Permissions, domain.KindWorker) {
			return "reviewer permissions more permissive than this project's own"
		}
	}
	return ""
}

// agentPermissionCap refuses (writing the 403) a config whose permission modes
// exceed the creator project's. It returns true when the request may go on.
func (c *ProjectsController) agentPermissionCap(w http.ResponseWriter, r *http.Request, agent requestscope.Agent, cfg domain.ProjectConfig) bool {
	if agent.DevMode {
		return true
	}
	var creator domain.ProjectConfig
	if got, err := c.Mgr.Get(r.Context(), agent.ProjectID); err == nil && got.Project != nil && got.Project.Config != nil {
		creator = *got.Project.Config
	}
	if why := permissionCapViolation(creator, cfg); why != "" {
		writeAgentScopeForbidden(w, r, "set "+why)
		return false
	}
	return true
}
