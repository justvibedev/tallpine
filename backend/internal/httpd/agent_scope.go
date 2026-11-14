package httpd

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/peeradmit"
	"tallpine.eu/tallpine/backend/internal/sessiontoken"
)

// AgentScopeStore resolves the session or review a scoped token claims.
type AgentScopeStore interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	GetReviewByID(ctx context.Context, id string) (domain.Review, bool, error)
}

// agentScopes authenticates scoped agent tokens (TALLPINE_SESSION_TOKEN) and
// authorizes them against agentRules. See
// docs/adr/0008-scoped-agent-session-tokens.md.
type agentScopes struct {
	tokens *sessiontoken.Authority
	store  AgentScopeStore
	log    *slog.Logger
	// approvals reports whether a project lets its orchestrator answer worker
	// prompts (the orchestratorApprovals agent policy). Nil means no project
	// does, which is the fail-closed default.
	approvals func(ctx context.Context, project domain.ProjectID) bool
	// crossProject reports whether a project lets its orchestrator coordinate
	// with other projects (the crossProjectOrchestration agent policy), decided
	// per request. Nil means no project does.
	crossProject func(ctx context.Context, project domain.ProjectID) bool
	// creators says which project's orchestrator registered a project. Nil
	// means none did, so no orchestrator may configure or start another project.
	creators controllers.ProjectCreatorStore
	// evalMarked reports whether a project is marked eval (ProjectConfig.Eval).
	// Nil means none is, so no orchestrator gets eval access.
	evalMarked func(ctx context.Context, project domain.ProjectID) bool
	// lanesEnabled reads the orchestratorLanes policy per request. Nil means off.
	lanesEnabled func(ctx context.Context, project domain.ProjectID) bool
	// devMode reads the owner-set devMode policy per request. Nil means off.
	devMode func(ctx context.Context, project domain.ProjectID) bool
	// peers checks who is presenting each token. Nil admits everyone.
	peers *peeradmit.Checker
}

func newAgentScopes(tokens *sessiontoken.Authority, store AgentScopeStore, log *slog.Logger) *agentScopes {
	if tokens == nil || store == nil {
		return nil
	}
	return &agentScopes{tokens: tokens, store: store, log: loggerOrDefault(log)}
}

// serve handles a request that presented a scoped token: it answers 401 for
// an unverifiable token, 403 for a call outside the principal's capabilities,
// and otherwise passes the request on with the principal on its context.
// Every refusal is written to the daemon log as an audit record.
func (s *agentScopes) serve(w http.ResponseWriter, r *http.Request, token string, next http.Handler) {
	agent, reason := s.authenticate(r.Context(), token)
	if reason != "" {
		s.audit(r, requestscope.Agent{}, token, "unauthenticated", reason)
		envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "SESSION_TOKEN_INVALID",
			"This session token is no longer valid: the session was relaunched or terminated, or the install token rotated. Restart the session's agent to get a fresh token.", nil)
		return
	}
	if !s.admitAgentPeer(w, r, agent) {
		return
	}
	if agent.Role == requestscope.RoleOrchestrator && s.crossProject != nil {
		// Decided from the live engine on every request, so a toggle in Settings
		// or a managed-policy change applies without a relaunch.
		agent.CrossProject = s.crossProject(r.Context(), agent.ProjectID)
		if agent.CrossProject {
			agent.Eval = evalChecker{scopes: s, creator: agent.ProjectID}
		}
	}
	if agent.Role == requestscope.RoleOrchestrator && s.devMode != nil && s.devMode(r.Context(), agent.ProjectID) && !devModeDenied(r.URL.Path) {
		agent.DevMode = true
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			s.auditDevMode(r, agent)
		}
		next.ServeHTTP(w, r.WithContext(requestscope.WithAgent(r.Context(), agent)))
		return
	}
	reason, cross := s.authorizeCross(r, agent)
	if reason != "" {
		s.audit(r, agent, "", "forbidden", reason)
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "AGENT_SCOPE_FORBIDDEN",
			"An agent session token cannot "+r.Method+" "+r.URL.Path+": "+reason+
				". Agent tokens act only on their own session (orchestrators: on their own project); ask the user to do this in "+brand.ProductName+".", nil)
		return
	}
	if cross != "" {
		s.auditCross(r, agent, cross)
	}
	next.ServeHTTP(w, r.WithContext(requestscope.WithAgent(r.Context(), agent)))
}

func (s *agentScopes) authenticate(ctx context.Context, token string) (requestscope.Agent, string) {
	kind, id, ok := sessiontoken.Parse(token)
	if !ok {
		return requestscope.Agent{}, "malformed token"
	}
	switch kind {
	case sessiontoken.KindSession:
		rec, found, err := s.store.GetSession(ctx, domain.SessionID(id))
		if err != nil || !found {
			return requestscope.Agent{}, "unknown session"
		}
		if !s.tokens.VerifySession(token, rec.ID, rec.Metadata.BrowserCapabilityVerifier) {
			return requestscope.Agent{}, "token does not match the session's current launch"
		}
		if rec.IsTerminated {
			return requestscope.Agent{}, "session is terminated"
		}
		role := requestscope.RoleWorker
		if rec.Kind == domain.KindOrchestrator {
			role = requestscope.RoleOrchestrator
		}
		return requestscope.Agent{Role: role, SessionID: rec.ID, ProjectID: rec.ProjectID}, ""
	case sessiontoken.KindReviewer:
		review, found, err := s.store.GetReviewByID(ctx, id)
		if err != nil || !found {
			return requestscope.Agent{}, "unknown review"
		}
		if !s.tokens.VerifyReviewer(token, review.ID, review.SessionID) {
			return requestscope.Agent{}, "token does not match the review"
		}
		worker, found, err := s.store.GetSession(ctx, review.SessionID)
		if err != nil || !found || worker.IsTerminated {
			return requestscope.Agent{}, "reviewed session is gone"
		}
		return requestscope.Agent{Role: requestscope.RoleReviewer, SessionID: worker.ID, ProjectID: worker.ProjectID, ReviewID: review.ID}, ""
	}
	return requestscope.Agent{}, "unknown token kind"
}

// authorizeCross returns "" when agent may make request r, else why not. For an allowed request that reaches outside
// the agent's own project (crossProjectOrchestration), a label for the audit
// record.
func (s *agentScopes) authorizeCross(r *http.Request, agent requestscope.Agent) (reason, cross string) {
	segments := routeSegments(r)
	routeKnown := false
	for _, rule := range agentRules {
		if !rule.allowsMethod(r.Method) {
			continue
		}
		param, ok := rule.match(segments)
		if !ok {
			continue
		}
		if !rule.allowsRole(agent.Role) {
			routeKnown = true
			continue
		}
		return s.checkTarget(r, rule.target, param, agent)
	}
	if routeKnown {
		return "the " + string(agent.Role) + " role has no access to this route", ""
	}
	return "the route is not available to agents", ""
}

// crossOffReason is the refusal for a cross-project action while the policy is
// off, so the agent (and the person reading its transcript) learns the setting
// exists and where it lives.
const crossOffReason = "cross-project orchestration is off for this project (agent policy crossProjectOrchestration, Settings > Agent policy)"

// maxScopedBody bounds the request bodies the gate reads to apply scope.
const maxScopedBody = 64 << 10

// checkTarget returns "" when the target is allowed, else why not; cross is
// non-empty for an allowed request outside the agent's own project.
func (s *agentScopes) checkTarget(r *http.Request, target agentTarget, param string, agent requestscope.Agent) (reason, cross string) {
	ctx := r.Context()
	switch target {
	case targetNone:
		return "", ""
	case targetSelf:
		if domain.SessionID(param) == agent.SessionID {
			return "", ""
		}
		return "it targets another session", ""
	case targetOwnProject:
		if param != "" && domain.ProjectID(param) == agent.ProjectID {
			return "", ""
		}
		return "it targets another project", ""
	case targetOwnReview:
		if param != "" && param == agent.ReviewID {
			return "", ""
		}
		return "it targets another review", ""
	case targetOwnOrEvalProject:
		if param != "" && domain.ProjectID(param) == agent.ProjectID {
			return "", ""
		}
		if agent.EvalAccess(ctx, domain.ProjectID(param)) {
			return "", "eval_project_access"
		}
		return "it targets another project (an orchestrator reaches only an eval project its own project created)", ""
	case targetOwnOrCreatedProject:
		if param != "" && domain.ProjectID(param) == agent.ProjectID {
			return "", ""
		}
		return s.checkCreatedProject(r, domain.ProjectID(param), agent)
	case targetCreatedProject:
		if param != "" && domain.ProjectID(param) == agent.ProjectID && agent.Role == requestscope.RoleOrchestrator {
			return ownConfigReason, ""
		}
		return s.checkCreatedProject(r, domain.ProjectID(param), agent)
	case targetNewProject:
		return s.checkNewProject(r, agent)
	case targetStartOrchestrator:
		return s.checkStartOrchestrator(r, agent)
	case targetEndLane:
		return s.checkEndLane(r, agent)
	}
	evalOK := false
	switch target {
	case targetVisibleOrEval:
		target, evalOK = targetVisible, true
	case targetMessageOrEval:
		target, evalOK = targetMessage, true
	case targetProjectOrEval:
		target, evalOK = targetProject, true
	}
	id := domain.SessionID(param)
	if target == targetWorkerTasks {
		return s.checkWorkerTasks(ctx, id, agent), ""
	}
	if target == targetWorkerApprovals {
		return s.checkWorkerApprovals(ctx, id, agent), ""
	}
	if id == agent.SessionID {
		return "", ""
	}
	rec, found, err := s.store.GetSession(ctx, id)
	if err == nil && !found {
		return "session " + string(id) + " was not found (it may have been deleted); do not escalate to the user", ""
	}
	if err != nil {
		return "it targets another session", ""
	}
	if agent.ProjectID == "" {
		return "it targets a session of another project", ""
	}
	if rec.ProjectID != agent.ProjectID {
		// A session of an eval project the caller's project created, on the
		// four routes that opt in (see targetVisibleOrEval).
		if evalOK && agent.EvalAccess(ctx, rec.ProjectID) {
			return "", "eval_project_access"
		}
		// Another project's orchestrator may be read and messaged, never its
		// workers or anything else, and only when the sender's own project opted
		// in. The recipient's setting is not consulted.
		if (target == targetVisible || target == targetMessage) && agent.Role == requestscope.RoleOrchestrator && rec.Kind == domain.KindOrchestrator {
			if agent.CrossProject {
				return "", "orchestrator_access"
			}
			return "it targets an orchestrator of another project: " + crossOffReason, ""
		}
		return "it targets a session of another project", ""
	}
	switch target {
	case targetVisible:
		if agent.CanSee(rec.ID, rec.ProjectID, rec.Kind) {
			return "", ""
		}
	case targetProject:
		if agent.Role == requestscope.RoleOrchestrator {
			return "", ""
		}
	case targetMessage:
		if agent.Role == requestscope.RoleOrchestrator || rec.Kind == domain.KindOrchestrator {
			return "", ""
		}
	}
	return "it targets another session", ""
}

func (s *agentScopes) audit(r *http.Request, agent requestscope.Agent, token, outcome, reason string) {
	attrs := []any{
		"audit", "agent_scope", "outcome", outcome, "reason", reason,
		"method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr,
	}
	if agent.Role != "" {
		attrs = append(attrs, "role", string(agent.Role), "session", string(agent.SessionID), "project", string(agent.ProjectID))
		if agent.ReviewID != "" {
			attrs = append(attrs, "review", agent.ReviewID)
		}
	} else if kind, id, ok := sessiontoken.Parse(token); ok {
		// The claimed id only: the token itself is a credential.
		attrs = append(attrs, "claimed_kind", string(kind), "claimed_id", id)
	}
	s.log.Warn("daemon refused an agent request", attrs...)
}

// agentTarget is what a rule's path parameter must name for the caller.
type agentTarget int

const (
	// targetNone: the route names no session; the role gate is the whole
	// check here, and the handler enforces body-level scope (see
	// controllers.scopedAgent).
	targetNone agentTarget = iota
	// targetSelf: the caller's own session (a reviewer's: the worker under
	// review).
	targetSelf
	// targetVisible: a session requestscope.Agent.CanSee allows.
	targetVisible
	// targetProject: workers their own session, orchestrators any session of
	// their project.
	targetProject
	// targetMessage: workers their project's orchestrators, orchestrators any
	// session of their project.
	targetMessage
	// targetWorkerApprovals: a worker session of the caller's project, not the
	// caller itself, while the project's orchestratorApprovals policy is on.
	targetWorkerApprovals
	// targetWorkerTasks: the caller's own session, or for an orchestrator a
	// worker session of its own project. Nothing else.
	targetWorkerTasks
	// targetOwnProject: the caller's own project id.
	targetOwnProject
	// targetOwnReview: a reviewer's own review session id.
	targetOwnReview
	// targetOwnOrCreatedProject: the caller's own project id, or for an
	// orchestrator whose project opted in (crossProjectOrchestration) a project
	// its own project created.
	targetOwnOrCreatedProject
	// targetOwnOrEvalProject: the caller's own project, or for an orchestrator
	// whose project opted in an eval project its own project created
	// (Agent.EvalAccess). Workers and reviewers get the own project only.
	targetOwnOrEvalProject
	// targetVisibleOrEval, targetMessageOrEval and targetProjectOrEval are the
	// base target plus, for an orchestrator, the sessions of an eval project its
	// own project created (Agent.EvalAccess). Only the rules for session get,
	// send, usage and kill use them; every other session route stays own-project.
	targetVisibleOrEval
	targetMessageOrEval
	targetProjectOrEval
	// targetCreatedProject: an opted-in orchestrator's created project only.
	targetCreatedProject
	// targetNewProject: an opted-in orchestrator registering a project; the
	// body is checked here.
	targetNewProject
	// targetStartOrchestrator: an opted-in orchestrator starting the
	// orchestrator of a project its own project created; the body is checked
	// here.
	targetStartOrchestrator
	// targetEndLane: the main orchestrator ending a lane of its own project
	// (policy orchestratorLanes).
	targetEndLane
)

var (
	workerUp     = []requestscope.AgentRole{requestscope.RoleWorker, requestscope.RoleOrchestrator}
	orchestrator = []requestscope.AgentRole{requestscope.RoleOrchestrator}
	reviewer     = []requestscope.AgentRole{requestscope.RoleReviewer}
	anyAgent     = []requestscope.AgentRole{requestscope.RoleWorker, requestscope.RoleOrchestrator, requestscope.RoleReviewer}
)

// agentRules is the complete allowlist for scoped agent tokens; every other
// route (including /mux, /shutdown, pprof, settings, mobile, shell terminals,
// account management, and every input resolution) is refused; so is every
// approval resolution except an opted-in project's orchestrator answering its
// workers.
// The first rule whose method and pattern match decides. A GET rule also
// allows HEAD. Patterns use chi syntax: {param} is one segment, a trailing *
// any remainder; the rule's single {param} is what its target checks.
var baseAgentRules = []agentRule{
	// Discovery.
	{"GET", "/api/v1/agents", workerUp, targetNone},
	{"POST", "/api/v1/agents/refresh", orchestrator, targetNone},
	{"POST", "/api/v1/agents/readiness/ensure", orchestrator, targetNone},
	{"GET", "/api/v1/projects", workerUp, targetNone}, // filtered to the own project
	{"GET", "/api/v1/projects/{id}", workerUp, targetOwnOrCreatedProject},
	{"GET", "/api/v1/orchestrators", workerUp, targetNone}, // filtered by CanSee
	{"GET", "/api/v1/orchestrators/{id}", workerUp, targetVisible},
	{"GET", "/api/v1/sessions", workerUp, targetNone}, // filtered by CanSee
	{"GET", "/api/v1/sessions/{sessionId}", workerUp, targetVisibleOrEval},
	{"GET", "/api/v1/sessions/{sessionId}", reviewer, targetSelf},

	// Silent-failure health: filtered to the own project by the controller.
	{"GET", "/api/v1/health/silent-failures", orchestrator, targetNone},

	// Reporting, activity, and coordination.
	{"POST", "/api/v1/reports", workerUp, targetNone},    // own session only
	{"GET", "/api/v1/reports", orchestrator, targetNone}, // own project only
	{"POST", "/api/v1/sessions/{sessionId}/activity", workerUp, targetSelf},
	{"GET", "/api/v1/sessions/{sessionId}/cleared-context-note", workerUp, targetSelf},
	// GitHub credentials for the helper mode of the agent policy, own session
	// only (a reviewer's own session is the worker under review). The daemon
	// re-decides the policy per request and audits every issue.
	{"POST", "/api/v1/sessions/{sessionId}/github-credential", anyAgent, targetSelf},
	// Commit in the own worktree for a sandboxed worker (fixed git argv, no push).
	{"POST", "/api/v1/sessions/{sessionId}/commit", workerUp, targetSelf},
	{"POST", "/api/v1/reviews/{reviewSessionID}/activity", reviewer, targetOwnReview},
	{"POST", "/api/v1/sessions/{sessionId}/send", workerUp, targetMessageOrEval},
	{"POST", "/api/v1/sessions/{sessionId}/conversation/steer-or-send", workerUp, targetMessage},
	{"POST", "/api/v1/sessions/{sessionId}/pr/claim", workerUp, targetProject},
	{"POST", "/api/v1/sessions/{sessionId}/background-tasks/stop", workerUp, targetWorkerTasks},
	// The PR controller checks the PR's owning session (merge: orchestrators
	// only, within their project).
	{"POST", "/api/v1/prs/{id}/resolve-comments", workerUp, targetNone},
	{"POST", "/api/v1/prs/{id}/merge", orchestrator, targetNone},
	{"POST", "/api/v1/prs/{id}/merge-preflight", orchestrator, targetNone},
	// Cues: create and list the own project's, never invoke or edit them.
	{"GET,POST", "/api/v1/projects/{projectId}/cues", workerUp, targetOwnProject},
	// Backlog: any agent files into the own project's inbox (a worker's POST is
	// forced to the inbox server-side) and reads it; only the orchestrator
	// edits, reorders, closes, links or sets fields.
	{"GET", "/api/v1/backlog/inbox-counts", orchestrator, targetNone}, // own project only
	{"GET,POST", "/api/v1/projects/{projectId}/backlog", workerUp, targetOwnOrEvalProject},
	// Merge digest: read-only view of recently merged and in-flight PRs.
	{"GET", "/api/v1/projects/{projectId}/merges", workerUp, targetOwnProject},
	// Durable orchestrator intents: only the orchestrator, own project.
	{"GET,POST", "/api/v1/projects/{projectId}/intents", orchestrator, targetOwnProject},
	{"POST", "/api/v1/projects/{projectId}/intents/{intentId}/done", orchestrator, targetOwnProject},
	// Backlog run mode: only the orchestrator reads or writes runs (the owner's
	// UI is not an agent); the daemon gate TALLPINE_BACKLOG_RUN answers 404 when off.
	{"GET,POST", "/api/v1/projects/{projectId}/backlog-runs", orchestrator, targetOwnProject},
	{"GET", "/api/v1/projects/{projectId}/backlog-runs/{runRef}", orchestrator, targetOwnProject},
	{"PUT", "/api/v1/projects/{projectId}/backlog-runs/{runRef}/plan", orchestrator, targetOwnProject},
	{"POST", "/api/v1/projects/{projectId}/backlog-runs/{runRef}/pause", orchestrator, targetOwnProject},
	{"POST", "/api/v1/projects/{projectId}/backlog-runs/{runRef}/resume", orchestrator, targetOwnProject},
	{"POST", "/api/v1/projects/{projectId}/backlog-runs/{runRef}/stop", orchestrator, targetOwnProject},
	{"POST", "/api/v1/projects/{projectId}/backlog-runs/{runRef}/finish", orchestrator, targetOwnProject},
	{"POST", "/api/v1/projects/{projectId}/backlog-runs/{runRef}/items/{ref}", orchestrator, targetOwnProject},
	{"GET", "/api/v1/projects/{projectId}/backlog/{ref}/usage", orchestrator, targetOwnProject},
	// Plans (B205): a worker reads; only the planner session recorded on the
	// plan submits (the service checks the session), the orchestrator starts,
	// answers, approves (with the owner's message) and re-plans.
	{"GET", "/api/v1/projects/{projectId}/backlog/{ref}/plan", workerUp, targetOwnOrEvalProject},
	{"POST", "/api/v1/projects/{projectId}/backlog/{ref}/plan/questions", workerUp, targetOwnProject},
	{"POST", "/api/v1/projects/{projectId}/backlog/{ref}/plan/submit", workerUp, targetOwnProject},
	{"POST", "/api/v1/projects/{projectId}/backlog/{ref}/plan", orchestrator, targetOwnOrEvalProject},
	{"POST", "/api/v1/projects/{projectId}/backlog/{ref}/plan/replan", orchestrator, targetOwnProject},
	{"POST", "/api/v1/projects/{projectId}/backlog/{ref}/plan/cancel", orchestrator, targetOwnProject},
	{"POST", "/api/v1/projects/{projectId}/backlog/{ref}/plan/answers", orchestrator, targetOwnOrEvalProject},
	{"POST", "/api/v1/projects/{projectId}/backlog/{ref}/plan/defaults", orchestrator, targetOwnOrEvalProject},
	{"POST", "/api/v1/projects/{projectId}/backlog/{ref}/plan/approve", orchestrator, targetOwnOrEvalProject},
	{"POST", "/api/v1/projects/{projectId}/backlog/{ref}/plan/build-anyway", orchestrator, targetOwnProject},
	// Owner asks (B698): only an orchestrator files, lists, relays an answer to
	// or withdraws an ask, own project; the owner's UI answers directly.
	{"GET,POST", "/api/v1/projects/{projectId}/asks", orchestrator, targetOwnProject},
	{"POST", "/api/v1/projects/{projectId}/asks/{ref}/answer", orchestrator, targetOwnProject},
	{"POST", "/api/v1/projects/{projectId}/asks/{ref}/withdraw", orchestrator, targetOwnProject},
	{"GET", "/api/v1/projects/{projectId}/backlog/next", workerUp, targetOwnProject},
	{"POST", "/api/v1/projects/{projectId}/backlog/reorder", orchestrator, targetOwnProject},
	{"GET", "/api/v1/projects/{projectId}/backlog/{ref}", workerUp, targetOwnOrEvalProject},
	{"PATCH,DELETE", "/api/v1/projects/{projectId}/backlog/{ref}", orchestrator, targetOwnProject},
	{"POST", "/api/v1/projects/{projectId}/backlog/purge", orchestrator, targetOwnProject},
	{"POST", "/api/v1/projects/{projectId}/backlog/{ref}/state", orchestrator, targetOwnOrEvalProject},
	{"POST,DELETE", "/api/v1/projects/{projectId}/backlog/{ref}/links", orchestrator, targetOwnProject},
	{"PUT,DELETE", "/api/v1/projects/{projectId}/backlog/{ref}/fields/{key}", orchestrator, targetOwnProject},
	{"POST", "/api/v1/sessions/{sessionId}/reviews/trigger", workerUp, targetProject},
	{"POST", "/api/v1/sessions/{sessionId}/reviews/cancel", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/reviews", reviewer, targetSelf},
	{"POST", "/api/v1/sessions/{sessionId}/reviews/submit", reviewer, targetSelf},
	{"GET", "/api/v1/sessions/{sessionId}/pr", reviewer, targetSelf},

	// Worker prompts: an orchestrator lists and resolves the pending approvals
	// of its project's workers only when the project opts in (policy
	// orchestratorApprovals, off by default, managed policy can force it off).
	// A worker's in-chat question (listed with the approvals) is answered the
	// same way. Side chats and reviewer conversations stay human-only.
	{"GET", "/api/v1/sessions/{sessionId}/conversation/approvals", orchestrator, targetWorkerApprovals},
	{"POST", "/api/v1/sessions/{sessionId}/conversation/approvals/{requestId}/resolve", orchestrator, targetWorkerApprovals},
	{"POST", "/api/v1/sessions/{sessionId}/conversation/inputs/{requestId}/resolve", orchestrator, targetWorkerApprovals},

	// Cross-project orchestration (policy crossProjectOrchestration, off by
	// default, decided live for the orchestrator's own project): register a
	// project, configure it and start its orchestrator, only for projects the
	// caller's project created. Worker and reviewer tokens never match.
	{"POST", "/api/v1/projects", orchestrator, targetNewProject},
	{"PUT", "/api/v1/projects/{id}", orchestrator, targetCreatedProject},
	{"PUT", "/api/v1/projects/{id}/config", orchestrator, targetCreatedProject},
	// An orchestrator may edit its own project's two rules text fields and
	// nothing else; the handler takes no other field. Workers may not: a
	// worker rewriting the instructions it and its peers run under would let
	// one misled worker persist directions across the project.
	{"PUT", "/api/v1/projects/{id}/rules", orchestrator, targetOwnProject},
	// Settings by key (B202): agents read the schema, app values and their own
	// project's values and change log (sensitive values redacted); an
	// orchestrator changes its own project's settings. The project is in the
	// query or body, so the handler and settingswrite check it, along with each
	// key's writers and tighten-only agent policy.
	{"GET", "/api/v1/settings/schema", workerUp, targetNone},
	{"GET", "/api/v1/settings/values", workerUp, targetNone},
	{"GET", "/api/v1/settings/changes", workerUp, targetNone},
	{"PATCH", "/api/v1/settings/values", orchestrator, targetNone},
	{"POST", "/api/v1/orchestrators", orchestrator, targetStartOrchestrator},
	{"POST", "/api/v1/orchestrators/end", orchestrator, targetEndLane},

	// Orchestration within the own project.
	{"POST", "/api/v1/sessions", orchestrator, targetNone},         // own project, worker kind, no approval override
	{"POST", "/api/v1/sessions/cleanup", orchestrator, targetNone}, // own project only
	// Handoff replaces the orchestrator itself, so only its own session may
	// ask; workers keep running.
	{"POST", "/api/v1/orchestrators/{id}/handoff", orchestrator, targetSelf},
	{"GET,POST", "/api/v1/orchestrators/{id}/research", orchestrator, targetSelf},
	{"POST", "/api/v1/sessions/{sessionId}/kill", orchestrator, targetProjectOrEval},
	{"POST", "/api/v1/sessions/{sessionId}/restore", orchestrator, targetProject},
	{"POST", "/api/v1/sessions/{sessionId}/exit-agent", orchestrator, targetProject},
	{"POST", "/api/v1/sessions/{sessionId}/resume-agent", orchestrator, targetProject},
	{"POST", "/api/v1/sessions/{sessionId}/rollback", orchestrator, targetProject},
	{"PATCH", "/api/v1/sessions/{sessionId}", orchestrator, targetProject},

	// Reading session state: workers their own, orchestrators their project's.
	{"GET", "/api/v1/sessions/{sessionId}/pr", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/pr/{prNumber}/files", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/pr/{prNumber}/file", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/pr/{prNumber}/file/revision", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/history", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/reviews", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/conversation", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/usage", workerUp, targetProjectOrEval},
	{"GET", "/api/v1/sessions/{sessionId}/interface-transition", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/workspace/manifest", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/workspace/history", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/workspace/branches", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/workspace/files", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/workspace/file", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/workspace/file/blob", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/workspace/file/revision", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/workspace/search", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/workspace/resolve", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/workspace/tree", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/workspace/events", workerUp, targetProject},
	{"POST", "/api/v1/sessions/{sessionId}/workspace/diffs", workerUp, targetProject},
	{"POST", "/api/v1/sessions/{sessionId}/workspace/history/selection", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/artifact-files/*", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/artifact-versions", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/artifact-versions/{version}", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/renders/{renderId}", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/preview", workerUp, targetProject},
	{"GET", "/api/v1/sessions/{sessionId}/preview/server", workerUp, targetProject},

	// The agent's own preview, browser, renders, and artifacts.
	{"POST,DELETE", "/api/v1/sessions/{sessionId}/preview", workerUp, targetSelf},
	{"GET", "/api/v1/sessions/{sessionId}/preview/file-url", workerUp, targetSelf},
	{"POST,DELETE", "/api/v1/sessions/{sessionId}/preview/server", workerUp, targetSelf},
	{"GET,POST,PUT,PATCH,DELETE", "/api/v1/sessions/{sessionId}/preview/app/*", workerUp, targetSelf},
	{"GET,POST", "/api/v1/sessions/{sessionId}/agent-browser/*", workerUp, targetSelf},
	{"POST", "/api/v1/sessions/{sessionId}/renders", workerUp, targetSelf},
	{"POST", "/api/v1/sessions/{sessionId}/renders/check", workerUp, targetSelf},
	{"POST", "/api/v1/sessions/{sessionId}/renders/{renderId}/artifact", workerUp, targetSelf},
	{"PUT,DELETE", "/api/v1/sessions/{sessionId}/artifact-files", workerUp, targetSelf},
	{"POST", "/api/v1/sessions/{sessionId}/artifact-versions/restore", workerUp, targetSelf},
	{"POST", "/api/v1/sessions/{sessionId}/artifact-versions/sync", workerUp, targetSelf},
	// The browser service checks TALLPINE_BROWSER_CAPABILITY, which is bound to
	// the caller's own session.
	{"GET", "/api/v1/browser/status", workerUp, targetNone},
	{"POST", "/api/v1/browser/commands", workerUp, targetNone},
}

type agentRule struct {
	methods string
	pattern string
	roles   []requestscope.AgentRole
	target  agentTarget
}

func (rule agentRule) allowsMethod(method string) bool {
	if method == http.MethodHead {
		method = http.MethodGet
	}
	for _, m := range strings.Split(rule.methods, ",") {
		if m == method {
			return true
		}
	}
	return false
}

func (rule agentRule) allowsRole(role requestscope.AgentRole) bool {
	for _, r := range rule.roles {
		if r == role {
			return true
		}
	}
	return false
}

// match reports whether path segments fit the rule's pattern and returns the
// (unescaped) value of its {param}, if any.
func (rule agentRule) match(segments []string) (string, bool) {
	pattern := strings.Split(strings.TrimPrefix(rule.pattern, "/"), "/")
	param := ""
	for i, want := range pattern {
		if want == "*" && i == len(pattern)-1 {
			return param, true
		}
		if i >= len(segments) {
			return "", false
		}
		got := segments[i]
		if strings.HasPrefix(want, "{") && strings.HasSuffix(want, "}") {
			if got == "" {
				return "", false
			}
			if i == firstParamIndex(pattern) {
				unescaped, err := url.PathUnescape(got)
				if err != nil {
					return "", false
				}
				param = unescaped
			}
			continue
		}
		if got != want {
			return "", false
		}
	}
	return param, len(segments) == len(pattern)
}

func firstParamIndex(pattern []string) int {
	for i, segment := range pattern {
		if strings.HasPrefix(segment, "{") {
			return i
		}
	}
	return -1
}

// routeSegments splits the path chi routes on: the escaped form when the URL
// carries one, so an encoded slash stays inside its segment exactly as chi
// sees it.
func routeSegments(r *http.Request) []string {
	path := r.URL.Path
	if r.URL.RawPath != "" {
		path = r.URL.RawPath
	}
	return strings.Split(strings.TrimPrefix(path, "/"), "/")
}
