package httpd

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/orchestratorapprovals"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

// orchestratorApprovalsEnabled reads the orchestratorApprovals policy of a
// project from the live engine on every request, so a managed-policy change or
// a toggle in Settings takes effect without a relaunch. No engine means off.
func orchestratorApprovalsEnabled(decider controllers.PolicyDecider) func(context.Context, domain.ProjectID) bool {
	if decider == nil {
		return nil
	}
	return func(ctx context.Context, project domain.ProjectID) bool {
		return orchestratorapprovals.Of(decider.Decide(ctx, agentpolicy.SessionRef{Project: project})).Enabled()
	}
}

// checkWorkerApprovals lets an orchestrator answer the prompts of a worker of
// its own project, and nothing else: never its own session, never another
// orchestrator, never another project, and only while the project's policy
// allows it. A worker or reviewer never reaches this (the rules list only the
// orchestrator role), so injected text cannot make another agent approve its
// escalation.
func (s *agentScopes) checkWorkerApprovals(ctx context.Context, id domain.SessionID, agent requestscope.Agent) string {
	if agent.Role != requestscope.RoleOrchestrator || id == agent.SessionID {
		return "an agent cannot answer its own prompts"
	}
	if s.approvals == nil || !s.approvals(ctx, agent.ProjectID) {
		return "orchestrators may not answer worker prompts in this project (Settings > Agent policy)"
	}
	rec, found, err := s.store.GetSession(ctx, id)
	if err != nil || !found || rec.Kind != domain.KindWorker || agent.ProjectID == "" || rec.ProjectID != agent.ProjectID {
		return "it targets a session that is not a worker of this project"
	}
	return ""
}
