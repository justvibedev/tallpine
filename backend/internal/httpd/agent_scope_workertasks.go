package httpd

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

// checkWorkerTasks lets an agent stop background tasks of its own session, and
// an orchestrator those of a worker of its own project; nothing else.
func (s *agentScopes) checkWorkerTasks(ctx context.Context, id domain.SessionID, agent requestscope.Agent) string {
	if id == agent.SessionID {
		return ""
	}
	if agent.Role != requestscope.RoleOrchestrator {
		return "it targets another session"
	}
	rec, found, err := s.store.GetSession(ctx, id)
	if err != nil || !found || rec.Kind != domain.KindWorker || agent.ProjectID == "" || rec.ProjectID != agent.ProjectID {
		return "it targets a session that is not a worker of this project"
	}
	return ""
}
