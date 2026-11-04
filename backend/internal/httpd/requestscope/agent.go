package requestscope

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// AgentRole is the capability set a scoped agent token carries.
type AgentRole string

const (
	// RoleWorker acts only on its own session (and may message its project's
	// orchestrator).
	RoleWorker AgentRole = "worker"
	// RoleOrchestrator may also spawn, message, read, and stop sessions of its
	// own project.
	RoleOrchestrator AgentRole = "orchestrator"
	// RoleReviewer may report its own activity and submit the review of the
	// one worker it reviews.
	RoleReviewer AgentRole = "reviewer"
)

// Agent is the principal behind a verified scoped token. A request without
// one was made with the install token (the desktop app, the user's CLI), or
// arrived through the LAN listener.
type Agent struct {
	Role      AgentRole
	SessionID domain.SessionID
	ProjectID domain.ProjectID
	// ReviewID is the reviewer's own review session; SessionID is then the
	// worker under review.
	ReviewID string
	// CrossProject is set for an orchestrator whose project has the
	// crossProjectOrchestration agent policy on, decided live for this request.
	// It is never set for a worker or reviewer.
	CrossProject bool
	// DevMode is set for an orchestrator while the owner-set devMode policy is on
	// (live, per request): it may do what the owner can, except change devMode.
	// Never set for a worker or reviewer.
	DevMode bool
	// Eval says which projects are both created by this agent's project and
	// marked eval (ProjectConfig.Eval). Set by the scope gate for an
	// orchestrator with CrossProject; nil otherwise, which means none.
	Eval EvalChecker
}

// EvalChecker reports whether a project is both created by the agent's project
// and marked eval. Implementations must be comparable (Agent is compared in tests).
type EvalChecker interface {
	EvalProject(ctx context.Context, project domain.ProjectID) bool
}

// EvalAccess reports whether this orchestrator may drive project as an eval
// project: it is not the agent's own project (that has its own rules), the
// agent is an orchestrator, and the project is created by the agent's project
// and marked eval. Workers and reviewers never pass.
func (a Agent) EvalAccess(ctx context.Context, project domain.ProjectID) bool {
	return a.Role == RoleOrchestrator && a.CrossProject && a.Eval != nil &&
		project != "" && project != a.ProjectID && a.Eval.EvalProject(ctx, project)
}

type agentKey struct{}

// WithAgent records the verified agent principal on a request context.
func WithAgent(ctx context.Context, agent Agent) context.Context {
	return context.WithValue(ctx, agentKey{}, agent)
}

// AgentFrom returns the verified agent principal, if the request carried a
// scoped token.
func AgentFrom(ctx context.Context) (Agent, bool) {
	agent, ok := ctx.Value(agentKey{}).(Agent)
	return agent, ok
}

// devOrchestrator is an orchestrator in Dev mode: it acts as the owner.
func (a Agent) devOrchestrator() bool { return a.DevMode && a.Role == RoleOrchestrator }

// MayActOn reports whether the agent may act on project's own-project-scoped
// resources: its own project, or any project for an orchestrator in Dev mode.
func (a Agent) MayActOn(project domain.ProjectID) bool {
	return project == a.ProjectID || a.devOrchestrator()
}

// CanSee reports whether the agent may read session s: its own session, any
// session of its project for an orchestrator, and its project's
// orchestrators for a worker (whom it may message). An orchestrator with
// CrossProject also sees the orchestrators (never the workers) of other projects.
func (a Agent) CanSee(id domain.SessionID, project domain.ProjectID, kind domain.SessionKind) bool {
	if id == a.SessionID || a.devOrchestrator() {
		return true
	}
	if a.CrossProject && a.Role == RoleOrchestrator && kind == domain.KindOrchestrator {
		return true
	}
	if project == "" || project != a.ProjectID {
		return false
	}
	switch a.Role {
	case RoleOrchestrator:
		return true
	case RoleWorker:
		return kind == domain.KindOrchestrator
	}
	return false
}
