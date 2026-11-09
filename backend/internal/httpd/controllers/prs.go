package controllers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	prsvc "tallpine.eu/tallpine/backend/internal/service/pr"
)

var prIDPattern = regexp.MustCompile(`^[1-9]\d*$`)

// PRsController owns the /prs action routes.
type PRsController struct {
	Svc prsvc.ActionManager
	// Scope resolves which session tracks a PR, so a scoped agent token acts
	// only on its own PRs (orchestrators: its project's). Nil refuses agents.
	Scope PRScopeStore
}

// PRScopeStore resolves a tracked PR's owning session the same way the PR
// actions resolve the PR they mutate: merge by URL, resolve by number.
type PRScopeStore interface {
	GetPR(ctx context.Context, url string) (domain.PullRequest, bool, error)
	GetPRByNumber(ctx context.Context, number int) (domain.PullRequest, bool, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// agentMayActOnPR reports whether a scoped agent may act on pr: a worker on
// its own session's PR, an orchestrator on any PR of its project.
func (c *PRsController) agentMayActOnPR(ctx context.Context, agent requestscope.Agent, pr domain.PullRequest, found bool, err error) bool {
	if err != nil || !found || pr.SessionID == "" {
		return false
	}
	if pr.SessionID == agent.SessionID {
		return true
	}
	if agent.Role != requestscope.RoleOrchestrator {
		return false
	}
	owner, ok, err := c.Scope.GetSession(ctx, pr.SessionID)
	return err == nil && ok && agent.ProjectID != "" && agent.MayActOn(owner.ProjectID)
}

// Register mounts the PR action routes on the supplied router.
func (c *PRsController) Register(r chi.Router) {
	r.Post("/prs/{id}/merge", c.merge)
	r.Post("/prs/{id}/merge-preflight", c.mergePreflight)
	r.Post("/prs/{id}/resolve-comments", c.resolveComments)
}

func (c *PRsController) merge(w http.ResponseWriter, r *http.Request) {
	c.mergeOrPreflight(w, r, false)
}

// mergePreflight runs every merge precondition on its own route, never the
// merge route, so a daemon that predates it answers 404 and nothing merges.
func (c *PRsController) mergePreflight(w http.ResponseWriter, r *http.Request) {
	c.mergeOrPreflight(w, r, true)
}

func (c *PRsController) mergeOrPreflight(w http.ResponseWriter, r *http.Request, preflight bool) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/prs/{id}/merge")
		return
	}
	prID := chi.URLParam(r, "id")
	if !prIDPattern.MatchString(prID) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "Invalid PR number", nil)
		return
	}
	var in MergePRRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if strings.TrimSpace(in.PRURL) == "" || strings.TrimSpace(in.ExpectedHeadSHA) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "PR URL and expected head SHA are required", nil)
		return
	}
	if agent, ok := scopedAgent(r); ok {
		if c.Scope == nil {
			writeAgentScopeForbidden(w, r, "merge pull requests here")
			return
		}
		pr, found, err := c.Scope.GetPR(r.Context(), in.PRURL)
		if !c.agentMayActOnPR(r.Context(), agent, pr, found, err) {
			writeAgentScopeForbidden(w, r, "merge a pull request outside its project")
			return
		}
	}
	var requester domain.SessionID
	if agent, ok := scopedAgent(r); ok {
		requester = agent.SessionID
	}
	res, err := c.Svc.Merge(r.Context(), prsvc.MergeRequest{PRID: prID, PRURL: in.PRURL, ExpectedHeadSHA: in.ExpectedHeadSHA, DryRun: preflight, Requester: requester})
	if err != nil {
		writePRError(w, r, err)
		return
	}
	if preflight {
		envelope.WriteJSON(w, http.StatusOK, MergePreflightResponse{OK: true, PRNumber: res.PRNumber})
		return
	}
	envelope.WriteJSON(w, http.StatusOK, MergePRResponse{
		OK:             true,
		PRNumber:       res.PRNumber,
		Method:         res.Method,
		State:          res.State,
		StateChangedAt: res.StateChangedAt,
	})
}

func (c *PRsController) resolveComments(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/prs/{id}/resolve-comments")
		return
	}
	prID := chi.URLParam(r, "id")
	if !prIDPattern.MatchString(prID) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "Invalid PR number", nil)
		return
	}

	// Body is optional: omitting it resolves all unresolved threads.
	var in ResolveCommentsRequest
	if err := decodeJSON(r, &in); err != nil && !isEmptyBody(err) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}

	if agent, ok := scopedAgent(r); ok {
		number, convErr := strconv.Atoi(prID)
		if c.Scope == nil || convErr != nil {
			writeAgentScopeForbidden(w, r, "resolve review threads here")
			return
		}
		pr, found, err := c.Scope.GetPRByNumber(r.Context(), number)
		if !c.agentMayActOnPR(r.Context(), agent, pr, found, err) {
			writeAgentScopeForbidden(w, r, "resolve review threads on another session's pull request")
			return
		}
	}
	res, err := c.Svc.ResolveComments(r.Context(), prID, in.CommentIDs)
	if err != nil {
		writePRError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ResolveCommentsResponse{OK: true, Resolved: res.Resolved})
}

// writePRError maps PR sentinel errors to their locked HTTP envelopes,
// falling back to 500 for unexpected failures.
func writePRError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, prsvc.ErrInvalidPR):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "Invalid PR", nil)
	case errors.Is(err, prsvc.ErrPRNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PR_NOT_FOUND", "Unknown PR", nil)
	case errors.Is(err, prsvc.ErrPRNotMergeable):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PR_NOT_MERGEABLE", "PR is not mergeable", nil)
	case errors.Is(err, prsvc.ErrPRHeadChanged):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PR_HEAD_CHANGED", "PR head changed; refresh before merging", nil)
	case errors.Is(err, prsvc.ErrPRPreconditions):
		message, details := preconditionErrorBody(err)
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "PR_PRECONDITIONS_UNMET", message, details)
	case errors.Is(err, prsvc.ErrNothingToResolve):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "NOTHING_TO_RESOLVE", "No unresolved review threads to resolve", nil)
	default:
		message, details := "PR operation failed", map[string]any(nil)
		if cause := safeErrorCause(err); cause != "" {
			message += ": " + cause
			details = map[string]any{"cause": cause}
		}
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "PR_OPERATION_FAILED", message, details)
	}
}

// maxErrorCauseRunes caps the cause text an API error carries.
const maxErrorCauseRunes = 500

// errorCauseRedactions are applied in order to an error's text before it is
// returned to API clients (it ends up in `tallpine pr merge` output and agent
// transcripts). The rules err on the side of redacting: an Authorization
// value is dropped whole, and any long opaque token-like run is removed even
// when that also hides a commit SHA; the full error stays in the daemon log.
var errorCauseRedactions = []struct {
	pattern *regexp.Regexp
	replace func(match []string) string
}{
	// A whole Authorization / Proxy-Authorization header value, whatever its
	// scheme ("token <hex>", "Bearer …", "Basic …", digest parameters),
	// including a quoted value as in JSON or a quoted header dump.
	{regexp.MustCompile(`(?i)\b((?:proxy-)?authorization)(["']?\s*[:=]\s*["']?)[^,;"']*`), func(m []string) string {
		return m[1] + m[2] + "[redacted]"
	}},
	// Credentials embedded in URLs (scheme://user[:password]@host). A bare
	// "git@" SSH user is not a secret and is kept for diagnostics.
	{regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)([^/\s@]+)@`), func(m []string) string {
		if strings.EqualFold(m[2], "git") {
			return m[0]
		}
		return m[1] + "[redacted]@"
	}},
	// Provider token shapes: every GitHub prefix (ghp_, gho_, ghu_, ghs_,
	// ghr_, github_pat_) and every GitLab prefix (glpat-, gloas-, gldt-,
	// glrt-, glcbt-, glptt-, glft-, glimt-, glsoat-, …), plus common
	// third-party API key shapes.
	{regexp.MustCompile(`\b(?:gh[a-z]_[A-Za-z0-9_]{8,}|github_pat_[A-Za-z0-9_]{8,}|(?:glpat|gloas|gldt|glrt|glcbt|glptt|glft|glimt|glsoat|glagent|gltst)-[A-Za-z0-9_-]{8,}|xox[a-z]-[A-Za-z0-9-]{8,}|sk-[A-Za-z0-9_-]{16,}|AKIA[0-9A-Z]{16})`), func([]string) string {
		return "[redacted]"
	}},
	// Any other GitLab-style "gl…-" token, as a net for prefixes not listed
	// above. The tail must contain a digit so hyphenated words such as
	// "global-configuration-settings" stay readable.
	{regexp.MustCompile(`\bgl[a-z]{1,6}-([A-Za-z0-9_-]{8,})`), func(m []string) string {
		if !strings.ContainsAny(m[1], "0123456789") {
			return m[0]
		}
		return "[redacted]"
	}},
	// Auth-scheme words followed by a credential ("token 0123…", "Bearer x").
	// The credential must contain a digit so prose such as "token rejected"
	// stays readable.
	{regexp.MustCompile(`(?i)\b(bearer|basic|token|bot|oauth|digest)\s+([A-Za-z0-9._~+/=-]+)`), func(m []string) string {
		if !strings.ContainsAny(m[2], "0123456789") {
			return m[0]
		}
		return m[1] + " [redacted]"
	}},
	// token/secret key-value pairs (query strings, JSON, form bodies).
	{regexp.MustCompile(`(?i)\b(access_token|refresh_token|id_token|private_token|client_secret|token|password|passwd|secret|api_key|apikey)(["']?\s*[:=]\s*["']?)[^\s&"',;]+`), func(m []string) string {
		return m[1] + m[2] + "[redacted]"
	}},
	// Base64 credentials, which may contain '/': a 32+ run of the base64
	// alphabet that also carries '+' or '=' padding (URL paths carry neither).
	{regexp.MustCompile(`[A-Za-z0-9+/]{32,}={0,2}`), func(m []string) string {
		if !strings.ContainsAny(m[0], "+=") || !strings.ContainsAny(m[0], "0123456789") {
			return m[0]
		}
		return "[redacted]"
	}},
	// Long opaque runs that look like unprefixed legacy or OAuth tokens: 32+
	// token characters mixing letters and digits.
	{regexp.MustCompile(`[A-Za-z0-9_+=-]{32,}`), func(m []string) string {
		if !strings.ContainsAny(m[0], "0123456789") || !strings.ContainsAny(m[0], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			return m[0]
		}
		return "[redacted]"
	}},
}

// safeErrorCause renders err as a single-line cause string that is safe to
// return to API clients: control characters are dropped, credentials are
// redacted (before truncation, so the cap cannot expose part of a secret) and
// the text is capped. It returns "" for a nil error.
func safeErrorCause(err error) string {
	if err == nil {
		return ""
	}
	cause := domain.SanitizeControlChars(err.Error())
	cause = strings.Join(strings.Fields(cause), " ")
	for _, r := range errorCauseRedactions {
		cause = r.pattern.ReplaceAllStringFunc(cause, func(match string) string {
			return r.replace(r.pattern.FindStringSubmatch(match))
		})
	}
	if runes := []rune(cause); len(runes) > maxErrorCauseRunes {
		cause = string(runes[:maxErrorCauseRunes]) + "…"
	}
	return cause
}

// preconditionErrorBody names the failed precondition. A typed merge blocker
// yields a stable `reason` code plus its description; any other wrapped
// precondition keeps its explanatory text as `detail`.
func preconditionErrorBody(err error) (string, map[string]any) {
	const message = "PR merge preconditions are not met"
	var pe *prsvc.PreconditionError
	if errors.As(err, &pe) {
		detail := pe.Blocker.Describe()
		return message + ": " + detail, map[string]any{"reason": string(pe.Blocker), "detail": detail}
	}
	prefix := prsvc.ErrPRPreconditions.Error() + ": "
	if !strings.HasPrefix(err.Error(), prefix) {
		return message, nil
	}
	detail := strings.TrimPrefix(err.Error(), prefix)
	return message + ": " + detail, map[string]any{"detail": detail}
}

// isEmptyBody reports whether err signals an absent or empty request body.
// io.ErrUnexpectedEOF means a truncated/malformed body — bad request, not absent.
func isEmptyBody(err error) bool {
	return errors.Is(err, io.EOF)
}
