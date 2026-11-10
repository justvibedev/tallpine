package controllers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/bodylimit"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/ports"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

func (c *SessionsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions")
		return
	}
	filter, err := parseSessionListFilter(r)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", err.Error(), nil)
		return
	}
	if agent, ok := scopedAgent(r); ok {
		project, allowed := scopeSessionListProject(r.Context(), agent, filter.ProjectID)
		if !allowed {
			envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "AGENT_SCOPE_FORBIDDEN",
				"agents may list only their own project's sessions", nil)
			return
		}
		filter.ProjectID = project
		filter.Standalone = false
	}
	page, err := c.Svc.ListPage(r.Context(), filter)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ListSessionsResponse{
		Sessions:   c.views(r.Context(), r, visibleToAgent(r, page.Sessions)),
		NextCursor: page.NextCursor,
		Archived:   archiveSummaryView(page.Archived),
	})
}

func archiveSummaryView(in *sessionsvc.ArchiveSummary) *SessionArchiveSummary {
	if in == nil {
		return nil
	}
	byProject := make(map[string]int, len(in.ByProject))
	for project, count := range in.ByProject {
		byProject[string(project)] = count
	}
	return &SessionArchiveSummary{Total: in.Total, ByProject: byProject}
}

func (c *SessionsController) spawn(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions")
		return
	}
	// Bound the body before decoding: this route is served on the LAN listener
	// (Tallpine Mobile), not just loopback, and the attachment caps only run after the
	// whole body is decoded. MaxBytesReader stops the read past the limit so an
	// oversized base64 payload can't allocate in full first.
	bodylimit.Raise(w, r, maxSpawnBodyBytes)
	var in SpawnSessionRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if !validClientRequestID(in.ClientRequestID) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "INVALID_CLIENT_REQUEST_ID", "clientRequestId must be 1-128 visible characters", nil)
		return
	}
	mode, err := domain.ParseSessionMode(string(in.Mode))
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "SESSION_MODE_INVALID", err.Error(), nil)
		return
	}
	in.Mode = mode
	if !in.ApprovalMode.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_APPROVAL_MODE", "approvalMode is invalid", nil)
		return
	}
	if !in.RequestedOutput.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "INVALID_REQUESTED_OUTPUT", "requestedOutput must be \"artifact\" or omitted", nil)
		return
	}
	if !in.Role.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "INVALID_ROLE", "role must be \"planner\" or omitted", nil)
		return
	}
	if in.Role == domain.SessionRolePlanner && (c.PlannerEnabled == nil || !c.PlannerEnabled()) {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PLANNER_DISABLED", "The planner is off (TALLPINE_PLANNER on this daemon)", nil)
		return
	}
	if in.Role != "" {
		if in.Kind == domain.KindOrchestrator || in.ProjectID == "" {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "INVALID_ROLE", "role is for project worker sessions", nil)
			return
		}
		in.RequestedOutput = domain.RequestedOutputArtifact
	}
	if len(in.Prompt) > maxPromptLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROMPT_TOO_LONG", "Prompt must be 16 KiB or fewer", nil)
		return
	}
	// displayName is optional at the API (the desktop new-task dialog omits it
	// and the read model falls back to the session id). `tallpine spawn` makes it
	// required CLI-side. When present, it is held to the same length cap here so
	// a direct API call cannot exceed it.
	displayName := strings.TrimSpace(in.DisplayName)
	if utf8.RuneCountInString(displayName) > maxDisplayNameLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "DISPLAY_NAME_TOO_LONG", fmt.Sprintf("displayName must be %d characters or fewer", maxDisplayNameLen), nil)
		return
	}
	if in.Kind == "" {
		in.Kind = domain.KindWorker
	}
	if agent, ok := scopedAgent(r); ok {
		if reason := scopeAgentSpawn(r.Context(), agent, &in); reason != "" {
			writeAgentScopeForbidden(w, r, reason)
			return
		}
	}
	attachments, attachErr := decodeSpawnAttachments(in.Attachments)
	if attachErr != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", attachErr.code, attachErr.message, nil)
		return
	}
	requestHash := ""
	if in.ClientRequestID != "" {
		keyless := in
		keyless.ClientRequestID = ""
		keyless.Attachments = nil
		requestHash = clientRequestFingerprint("session", keyless, attachments)
	}
	if in.BacklogPlan != "" && (strings.TrimSpace(in.BacklogRef) == "" || !backlogsvc.ValidPlanArm(in.BacklogPlan)) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "BACKLOG_PLAN_INVALID", "backlogPlan is in-place or separate and needs backlogRef", nil)
		return
	}
	if strings.TrimSpace(in.BacklogRef) != "" && in.ProjectID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "BACKLOG_PROJECT_REQUIRED", "backlogRef needs a project session", nil)
		return
	}
	backlogItem, warnings, ok := c.prepareBacklogSpawn(w, r, "POST", "/api/v1/sessions", in.ProjectID, in.BacklogRef, in.BacklogForce, in.BacklogAllowDuplicate, in.BacklogAllowBlocked, in.BacklogPlan)
	if !ok {
		return
	}
	sess, promptBytes, systemPromptBytes, err := c.Svc.Spawn(r.Context(), ports.SpawnConfig{BacklogContext: backlogItem.Context, ProjectID: in.ProjectID, IssueID: in.IssueID, ParentSessionID: in.ParentSessionID, LaneItemID: backlogItem.ParentID, TrackerProvider: in.TrackerProvider, Kind: in.Kind, Harness: in.Harness, Branch: in.Branch, RequestedMode: in.Mode, Prompt: in.Prompt, RequestedOutput: in.RequestedOutput, Role: in.Role, DisplayName: displayName, Attachments: attachments, AgentConfig: ports.AgentConfig{Model: in.Model, Effort: in.Effort, Permissions: in.ApprovalMode}, EffortOverride: strings.TrimSpace(in.Effort) != "", ClientRequestID: in.ClientRequestID, ClientRequestHash: requestHash})
	if err != nil {
		if backlogItem.PlanID != "" {
			c.Backlog.ReleaseInPlace(r.Context(), backlogItem.PlanID, "implementer session did not start: "+err.Error())
		}
		envelope.WriteError(w, r, err)
		return
	}
	if backlogItem.PlanID != "" {
		if err := c.Backlog.AttachInPlaceAuthor(r.Context(), backlogItem.PlanID, sess.ID); err != nil {
			slog.Warn("attach in-place plan author failed", "item", backlogItem.Ref, "session", sess.ID, "error", err)
			warnings = append(warnings, fmt.Sprintf("session %s started but could not be recorded as the plan author: %v", sess.ID, err))
		}
	}
	if c.Routes != nil && in.Kind == domain.KindWorker && in.ProjectID != "" {
		if notice := c.Routes.RouteNotice(r.Context(), in.ProjectID, "", in.ParentSessionID); notice != "" {
			warnings = append(warnings, notice)
		}
	}
	warnings = c.linkBacklogSession(r, backlogItem, sess.ID, string(in.ParentSessionID), warnings)
	envelope.WriteJSON(w, http.StatusCreated, SpawnSessionResponse{Session: c.view(r.Context(), r, sess), PromptBytes: promptBytes, SystemPromptBytes: systemPromptBytes, Warnings: warnings})
}

// BacklogSpawner is the backlog side of `tallpine spawn --backlog`.
type BacklogSpawner interface {
	PrepareSpawnPlan(ctx context.Context, projectID domain.ProjectID, ref string, force, allowDuplicate, allowBlocked bool, arm string) (backlogsvc.SpawnItem, error)
	// AttachInPlaceAuthor and ReleaseInPlace settle the plan PrepareSpawnPlan
	// reserved for an in-place implementer.
	AttachInPlaceAuthor(ctx context.Context, planID string, sid domain.SessionID) error
	ReleaseInPlace(ctx context.Context, planID, reason string)
	LinkSession(ctx context.Context, itemID string, sessionID domain.SessionID, createdBy string) error
	// ResolveLane returns the item id an orchestrator lane is bound to.
	ResolveLane(ctx context.Context, projectID domain.ProjectID, ref string) (string, error)
	// ResolveLaneItem is ResolveLane without refusing a closed item: it returns
	// the item's state when closed, so a running lane orchestrator stays reachable.
	ResolveLaneItem(ctx context.Context, projectID domain.ProjectID, ref string) (string, domain.BacklogState, error)
}

func (c *SessionsController) listPRs(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/pr")
		return
	}
	listing, err := c.Svc.ListPRListing(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	linked := make([]SessionPRReference, len(listing.Linked))
	for i, ref := range listing.Linked {
		linked[i] = SessionPRReference{URL: ref.URL, Provider: ref.Provider, Host: ref.Host, Repo: ref.Repository, Number: ref.Number}
	}
	envelope.WriteJSON(w, http.StatusOK, ListSessionPRsResponse{SessionID: sessionID(r), PRs: sessionPRSummaries(listing.Tracked), LinkedPRs: linked})
}

func (c *SessionsController) claimPR(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/pr/claim")
		return
	}
	var in ClaimPRRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if strings.TrimSpace(in.PR) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PR_REQUIRED", "pr is required", nil)
		return
	}
	allowTakeover := true
	if in.AllowTakeover != nil {
		allowTakeover = *in.AllowTakeover
	}
	opts := sessionsvc.ClaimPROptions{AllowTakeover: allowTakeover, RecordInteraction: true}
	if agent, ok := scopedAgent(r); ok {
		opts.TakeoverGuard = agentClaimTakeoverGuard(agent)
		opts.RecordInteraction = agent.SessionID != sessionID(r)
	}
	res, err := c.Svc.ClaimPR(r.Context(), sessionID(r), in.PR, opts)
	if err != nil {
		writeSessionPRError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ClaimPRResponse{OK: true, SessionID: sessionID(r), PRs: sessionPRFacts(res.PRs), BranchChanged: res.BranchChanged, TakenOverFrom: nonNilSessionIDs(res.TakenOverFrom)})
}

func (c *SessionsController) restore(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/restore")
		return
	}
	out, err := c.Svc.Restore(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, RestoreSessionResponse{OK: true, SessionID: sessionID(r), RestoreMode: out.Mode, Session: c.view(r.Context(), r, out.Session)})
}

func (c *SessionsController) pin(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/pin")
		return
	}
	sess, err := c.Svc.Pin(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: c.view(r.Context(), r, sess)})
}

func (c *SessionsController) unpin(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/sessions/{sessionId}/pin")
		return
	}
	sess, err := c.Svc.Unpin(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: c.view(r.Context(), r, sess)})
}

func (c *SessionsController) resumeAgent(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/resume-agent")
		return
	}
	out, err := c.Svc.ResumeAgent(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ResumeAgentResponse{
		OK:         true,
		SessionID:  sessionID(r),
		ResumeMode: out.Mode,
		Session:    c.view(r.Context(), r, out.Session),
	})
}

func (c *SessionsController) exitAgent(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/exit-agent")
		return
	}
	out, err := c.Svc.ExitAgent(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ExitAgentResponse{
		OK:        true,
		SessionID: sessionID(r),
		Session:   c.view(r.Context(), r, out.Session),
	})
}

func (c *SessionsController) kill(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/kill")
		return
	}
	var in KillSessionRequest
	if err := decodeJSON(r, &in); err != nil && !errors.Is(err, io.EOF) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	result, err := c.Svc.RequestKill(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	// The session is already archived here, so a failed delete must not fail
	// the request: the caller would roll back an archive that happened. The
	// response reports it instead and the files stay on disk.
	artifactsDeleted := false
	if in.DeleteArtifacts {
		if err := c.Svc.DeleteArtifacts(r.Context(), sessionID(r)); err != nil {
			slog.WarnContext(r.Context(), "delete session artifacts after archive", "session", sessionID(r), "err", err)
		} else {
			artifactsDeleted = true
		}
	}
	envelope.WriteJSON(w, http.StatusOK, KillSessionResponse{OK: true, SessionID: sessionID(r), Freed: result.Freed, CleanupPending: result.CleanupPending, ArtifactsDeleted: artifactsDeleted})
}

// rollback undoes a partially-completed spawn: if the session row is still in
// seed state (no workspace, no runtime handle yet), the row is deleted
// outright. If anything observable has landed it falls back to Kill so the
// runtime/workspace are torn down. Used by `tallpine spawn --claim-pr` to undo a
// session whose claim step failed, avoiding the orphan terminated row a
// plain Kill would leave behind.
func (c *SessionsController) rollback(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/rollback")
		return
	}
	out, err := c.Svc.RollbackSpawn(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, RollbackSessionResponse{OK: true, SessionID: sessionID(r), Deleted: out.Deleted, Killed: out.Killed})
}

func (c *SessionsController) cleanup(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/cleanup")
		return
	}
	project := domain.ProjectID(r.URL.Query().Get("project"))
	if agent, ok := scopedAgent(r); ok {
		if project != "" && !agent.MayActOn(project) {
			writeAgentScopeForbidden(w, r, "clean up sessions of another project")
			return
		}
		if project == "" {
			project = agent.ProjectID
		}
	}
	out, err := c.Svc.Cleanup(r.Context(), project)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	skipped := make([]CleanupSkippedSession, 0, len(out.Skipped))
	for _, skip := range out.Skipped {
		skipped = append(skipped, CleanupSkippedSession{SessionID: skip.SessionID, Reason: skip.Reason})
	}
	envelope.WriteJSON(w, http.StatusOK, CleanupSessionsResponse{
		OK: true, Cleaned: out.Cleaned, AlreadyGone: out.AlreadyGone, Skipped: skipped,
	})
}

func parseSessionListFilter(r *http.Request) (sessionsvc.ListFilter, error) {
	q := r.URL.Query()
	filter := sessionsvc.ListFilter{ProjectID: domain.ProjectID(q.Get("project"))}
	if raw := q.Get("active"); raw != "" {
		active, err := strconv.ParseBool(raw)
		if err != nil {
			return sessionsvc.ListFilter{}, errors.New("active must be a boolean")
		}
		filter.Active = &active
	}
	if raw := q.Get("orchestratorOnly"); raw != "" {
		orchestratorOnly, err := strconv.ParseBool(raw)
		if err != nil {
			return sessionsvc.ListFilter{}, errors.New("orchestratorOnly must be a boolean")
		}
		filter.OrchestratorOnly = orchestratorOnly
	}
	if raw := q.Get("fresh"); raw != "" {
		fresh, err := strconv.ParseBool(raw)
		if err != nil {
			return sessionsvc.ListFilter{}, errors.New("fresh must be a boolean")
		}
		filter.Fresh = fresh
	}
	if raw := q.Get("standalone"); raw != "" {
		standalone, err := strconv.ParseBool(raw)
		if err != nil {
			return sessionsvc.ListFilter{}, errors.New("standalone must be a boolean")
		}
		filter.Standalone = standalone
	}
	switch kind := domain.SessionKind(q.Get("kind")); kind {
	case "":
	case domain.KindWorker, domain.KindOrchestrator:
		filter.Kind = kind
	default:
		return sessionsvc.ListFilter{}, errors.New("kind must be worker or orchestrator")
	}
	if raw := q.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > sessionsvc.MaxListLimit {
			return sessionsvc.ListFilter{}, fmt.Errorf("limit must be an integer from 1 to %d", sessionsvc.MaxListLimit)
		}
		filter.Limit = limit
	}
	if filter.Cursor = q.Get("cursor"); filter.Cursor != "" && filter.Limit == 0 {
		return sessionsvc.ListFilter{}, errors.New("cursor requires limit")
	}
	return filter, nil
}

func writeSessionPRError(w http.ResponseWriter, r *http.Request, err error) {
	var claimed ports.PRClaimedByActiveSessionError
	switch {
	case errors.Is(err, sessionsvc.ErrInvalidPRRef):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR_REF", "PR reference must be a full PR/MR URL or a number resolved against the project origin or canonical repository. For a workspace child repository, pass its full PR/MR URL; a root without a remote cannot resolve numbers.", nil)
	case errors.Is(err, sessionsvc.ErrPRNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PR_NOT_FOUND", "Unknown PR", nil)
	case errors.Is(err, sessionsvc.ErrPRNotOpen):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PR_NOT_OPEN", "PR is not open", nil)
	case errors.As(err, &claimed):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PR_CLAIMED_BY_ACTIVE_SESSION", "PR is already claimed by active session "+string(claimed.Owner)+" (omit --no-takeover to steal)", map[string]any{"ownerSessionId": string(claimed.Owner)})
	case errors.Is(err, sessionsvc.ErrSessionNotClaimable):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SESSION_NOT_CLAIMABLE", "Session cannot claim PRs", nil)
	case errors.Is(err, sessionsvc.ErrSessionNoWorkspace):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SESSION_NO_WORKSPACE", "Session has no workspace", nil)
	case errors.Is(err, sessionsvc.ErrProjectMismatch):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "PR_PROJECT_MISMATCH", "PR repository must match the project origin, its explicit canonicalRepoURL, or a registered workspace child origin. For workspace projects, check the registered repositories with tallpine project get <project-id> --json and pass the child's full PR/MR URL. canonicalRepoURL requires a valid root origin and is not a child-repository allowlist. For single-repo forks, configure the upstream HTTPS repository URL with tallpine project set-config <project-id> --canonical-repo-url <url> (merges into the existing config). Git remotes alone do not grant trust.", nil)
	case errors.Is(err, sessionsvc.ErrSCMUnavailable):
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "SCM_UNAVAILABLE", "SCM unavailable", nil)
	default:
		envelope.WriteError(w, r, err)
	}
}
