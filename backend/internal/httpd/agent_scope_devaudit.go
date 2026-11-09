package httpd

import (
	"log/slog"
	"net/http"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

// Scope values of a Dev mode audit line. Only what the route's session or
// project path parameter establishes is own_project or cross_project; anything
// else (bodies, queries, PRs, reviews) is deliberately unknown.
const (
	devScopeOwn     = "own_project"
	devScopeCross   = "cross_project"
	devScopeUnknown = "unknown"
)

// auditDevMode logs every Dev mode write, so none is lost. Dev mode lifts the
// scope gate, so the line says what the request is known to reach.
func (s *agentScopes) auditDevMode(r *http.Request, agent requestscope.Agent) {
	scope := s.devModeScope(r, agent)
	if scope == devScopeCross {
		s.auditCross(r, agent, "dev_mode")
		return
	}
	// Warn for unknown, like the cross-project line: it may be cross-project.
	level := slog.LevelWarn
	if scope == devScopeOwn {
		level = slog.LevelInfo
	}
	s.log.Log(r.Context(), level, "daemon allowed an agent write in dev mode",
		"audit", "agent_scope", "outcome", "dev_mode_allowed", "scope", scope,
		"method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr,
		"role", string(agent.Role), "session", string(agent.SessionID), "project", string(agent.ProjectID))
}

// devModeScope resolves the route's session or project parameter through the
// existing rule table and session lookup, and nothing more.
func (s *agentScopes) devModeScope(r *http.Request, agent requestscope.Agent) string {
	segments := routeSegments(r)
	for _, rule := range agentRules {
		if !rule.allowsMethod(r.Method) {
			continue
		}
		param, ok := rule.match(segments)
		if !ok {
			continue
		}
		if param == "" {
			return devScopeUnknown
		}
		switch rule.target {
		case targetOwnProject, targetOwnOrEvalProject, targetOwnOrCreatedProject, targetCreatedProject:
			return projectScope(domain.ProjectID(param), agent)
		case targetNone, targetNewProject, targetStartOrchestrator, targetEndLane, targetOwnReview:
			return devScopeUnknown
		}
		return s.sessionScope(r, param, agent)
	}
	// Dev mode reaches routes the rule table omits (deleting a project or a
	// session); the path shape still names the target.
	if len(segments) >= 4 && segments[3] != "" {
		switch segments[2] {
		case "projects":
			// Literal routes (clone, initialize) name no project.
			if segments[3] == "clone" || segments[3] == "initialize" {
				return devScopeUnknown
			}
			return projectScope(domain.ProjectID(segments[3]), agent)
		case "sessions":
			return s.sessionScope(r, segments[3], agent)
		}
	}
	return devScopeUnknown
}

func (s *agentScopes) sessionScope(r *http.Request, id string, agent requestscope.Agent) string {
	rec, found, err := s.store.GetSession(r.Context(), domain.SessionID(id))
	if err != nil || !found {
		return devScopeUnknown
	}
	return projectScope(rec.ProjectID, agent)
}

func projectScope(p domain.ProjectID, agent requestscope.Agent) string {
	if p == agent.ProjectID {
		return devScopeOwn
	}
	return devScopeCross
}
