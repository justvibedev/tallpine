package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/ports"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	reviewsvc "tallpine.eu/tallpine/backend/internal/service/review"
)

// CancelReviewResponse is the body of cancel (200). reviews carries the
// PR-scoped review state after running passes have been stopped.
type CancelReviewResponse struct {
	ReviewerHandleID string                     `json:"reviewerHandleId"`
	Reviews          []reviewcore.PRReviewState `json:"reviews"`
}

// RestoreReviewResponse is the body of reviewer session restore (200).
type RestoreReviewResponse struct {
	ReviewerHandleID string                     `json:"reviewerHandleId"`
	ReviewerHarness  domain.ReviewerHarness     `json:"reviewerHarness,omitempty"`
	Reviews          []reviewcore.PRReviewState `json:"reviews"`
	Runs             []domain.ReviewRun         `json:"runs"`
}

// KillReviewResponse is the body of reviewer session kill (200).
type KillReviewResponse struct {
	ReviewerHandleID string                     `json:"reviewerHandleId"`
	ReviewerHarness  domain.ReviewerHarness     `json:"reviewerHarness,omitempty"`
	Reviews          []reviewcore.PRReviewState `json:"reviews"`
	Runs             []domain.ReviewRun         `json:"runs"`
}

// ReviewsController owns the session-scoped /reviews routes. A nil Svc returns 501.
type ReviewsController struct {
	Svc reviewsvc.Manager
}

// Register mounts the review routes on the supplied router.
func (c *ReviewsController) Register(r chi.Router) {
	r.Post("/reviews/{reviewSessionID}/activity", c.activity)
	r.Get("/sessions/{sessionId}/reviews", c.list)
	r.Post("/sessions/{sessionId}/reviews/trigger", c.trigger)
	r.Post("/sessions/{sessionId}/reviews/rerequest", c.rerequest)
	r.Post("/sessions/{sessionId}/reviews/comments/resolve", c.resolveComment)
	r.Post("/sessions/{sessionId}/reviews/cancel", c.cancel)
	r.Post("/sessions/{sessionId}/reviews/kill", c.kill)
	r.Post("/sessions/{sessionId}/reviews/restore", c.restore)
	r.Post("/sessions/{sessionId}/reviews/switch", c.switchReviewer)
	r.Post("/sessions/{sessionId}/reviews/submit", c.submit)
}

func (c *ReviewsController) activity(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/reviews/{reviewSessionID}/activity")
		return
	}
	reviewSessionID := strings.TrimSpace(chi.URLParam(r, "reviewSessionID"))
	if reviewSessionID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "REVIEW_ACTIVITY_INVALID", "Review session id is required", nil)
		return
	}
	var in SetReviewActivityRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	state := domain.ActivityState(strings.TrimSpace(in.State))
	if state != "" {
		switch state {
		case domain.ActivityActive, domain.ActivityIdle, domain.ActivityWaitingInput, domain.ActivityBlocked, domain.ActivityExited:
		default:
			// Reviewer hooks are best-effort. If a reviewer CLI stops emitting one
			// of Tallpine's known activity states, degrade to a no-op instead of turning
			// review-run polling into a surfaced hook failure.
			state = ""
		}
	}
	agentSessionID := capActivityMeta(domain.SanitizeControlChars(strings.TrimSpace(in.AgentSessionID)))
	if state == "" && agentSessionID == "" {
		envelope.WriteJSON(w, http.StatusOK, SetReviewActivityResponse{OK: true, ReviewSessionID: reviewSessionID})
		return
	}
	if err := c.Svc.ApplyReviewActivitySignal(r.Context(), reviewSessionID, reviewsvc.ActivitySignal{
		Event:          capActivityMeta(domain.SanitizeControlChars(in.Event)),
		State:          state,
		AgentSessionID: agentSessionID,
		LaunchID:       capActivityMeta(domain.SanitizeControlChars(strings.TrimSpace(in.LaunchID))),
	}); err != nil {
		if errors.Is(err, reviewsvc.ErrNotFound) {
			envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "REVIEW_NOT_FOUND", "Unknown review session", nil)
			return
		}
		writeReviewError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SetReviewActivityResponse{OK: true, ReviewSessionID: reviewSessionID})
}

func (c *ReviewsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/reviews")
		return
	}
	res, err := c.Svc.List(r.Context(), sessionID(r))
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	reviews := res.Reviews
	if reviews == nil {
		reviews = []reviewcore.PRReviewState{}
	}
	runs := res.Runs
	if runs == nil {
		runs = []domain.ReviewRun{}
	}
	envelope.WriteJSON(w, http.StatusOK, reviewsResponse(res, reviews, runs))
}

func (c *ReviewsController) trigger(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/reviews/trigger")
		return
	}
	// Body is optional: omitting it runs under the project's configured reviewer.
	var in TriggerReviewRequest
	if err := decodeJSON(r, &in); err != nil && !isEmptyBody(err) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if _, ok := scopedAgent(r); ok {
		// An agent-requested pass is never recorded as the user's.
		in.Source = string(domain.ReviewTriggerAgent)
	}
	treq := reviewsvc.TriggerRequest{
		Harness:            in.Harness,
		Config:             in.AgentConfig,
		Source:             domain.ReviewTriggerSource(strings.TrimSpace(in.Source)),
		InterfaceMode:      in.InterfaceMode,
		PRURL:              strings.TrimSpace(in.PRURL),
		Head:               strings.TrimSpace(in.Head),
		RejectReviewedHead: in.RejectReviewedHead,
		Rerun:              in.Rerun,
		EnableAutoInject:   in.EnableAutoInject,
	}
	res, err := c.Svc.TriggerRequested(r.Context(), sessionID(r), treq)
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	// 201 when a new pass was started; 200 when an existing run for the same
	// commit was reused.
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	reviews := res.Reviews
	if reviews == nil {
		reviews = []reviewcore.PRReviewState{}
	}
	runs := res.Runs
	if runs == nil {
		runs = []domain.ReviewRun{}
	}
	envelope.WriteJSON(w, status, TriggerReviewResponse{
		ReviewerHandleID:  res.ReviewerHandleID,
		Reviews:           reviews,
		Runs:              runs,
		Created:           res.Created,
		AutoInjectEnabled: res.AutoInjectEnabled,
		ReviewerSurface:   reviewerSurfacePayload(res.ReviewerSurface),
	})
}

func (c *ReviewsController) resolveComment(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/reviews/comments/resolve")
		return
	}
	var in ResolveReviewCommentRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if err := c.Svc.ResolveReviewComment(r.Context(), sessionID(r), in.PullRequestURL, in.CommentURL); err != nil {
		writeReviewError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ResolveReviewCommentResponse{OK: true})
}

func (c *ReviewsController) rerequest(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/reviews/rerequest")
		return
	}
	var in RequestRereviewRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if err := c.Svc.RequestRereview(r.Context(), sessionID(r), in.PullRequestURL, in.ReviewerID); err != nil {
		writeReviewError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, RequestRereviewResponse{OK: true})
}

func (c *ReviewsController) cancel(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/reviews/cancel")
		return
	}
	res, err := c.Svc.Cancel(r.Context(), sessionID(r))
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	reviews := res.Reviews
	if reviews == nil {
		reviews = []reviewcore.PRReviewState{}
	}
	envelope.WriteJSON(w, http.StatusOK, CancelReviewResponse{
		ReviewerHandleID: res.ReviewerHandleID,
		Reviews:          reviews,
	})
}

func (c *ReviewsController) kill(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/reviews/kill")
		return
	}
	workerID := sessionID(r)
	if err := c.Svc.ArchiveReviewer(r.Context(), workerID); err != nil {
		writeReviewError(w, r, err)
		return
	}
	res, err := c.Svc.List(r.Context(), workerID)
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	response := reviewsResponse(res, nil, nil)
	envelope.WriteJSON(w, http.StatusOK, KillReviewResponse{ReviewerHandleID: response.ReviewerHandleID, ReviewerHarness: response.ReviewerHarness, Reviews: response.Reviews, Runs: response.Runs})
}

func (c *ReviewsController) restore(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/reviews/restore")
		return
	}
	workerID := sessionID(r)
	if err := c.Svc.RestoreReviewer(r.Context(), workerID); err != nil {
		writeReviewError(w, r, err)
		return
	}
	res, err := c.Svc.List(r.Context(), workerID)
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	reviews := res.Reviews
	if reviews == nil {
		reviews = []reviewcore.PRReviewState{}
	}
	runs := res.Runs
	if runs == nil {
		runs = []domain.ReviewRun{}
	}
	envelope.WriteJSON(w, http.StatusOK, RestoreReviewResponse{ReviewerHandleID: res.ReviewerHandleID, ReviewerHarness: res.ReviewerHarness, Reviews: reviews, Runs: runs})
}

func (c *ReviewsController) switchReviewer(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/reviews/switch")
		return
	}
	var in SetSessionReviewerRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	res, err := c.Svc.SwitchReviewer(r.Context(), sessionID(r), in.Harness, in.AgentConfig)
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, reviewsResponse(res, nil, nil))
}

func reviewsResponse(res reviewcore.SessionReviews, reviews []reviewcore.PRReviewState, runs []domain.ReviewRun) ListReviewsResponse {
	if reviews == nil {
		reviews = res.Reviews
	}
	if reviews == nil {
		reviews = []reviewcore.PRReviewState{}
	}
	if runs == nil {
		runs = res.Runs
	}
	if runs == nil {
		runs = []domain.ReviewRun{}
	}
	return ListReviewsResponse{
		ReviewerHandleID:      res.ReviewerHandleID,
		ReviewerHarness:       res.ReviewerHarness,
		ReviewerActivityState: string(res.ReviewerActivityState),
		Reviews:               reviews,
		Runs:                  runs,
		ReviewerSurface:       reviewerSurfacePayload(res.ReviewerSurface),
		ActiveReviewers:       activeReviewersPayload(res.ActiveReviewers),
	}
}

func activeReviewersPayload(surfaces []domain.ReviewerSurface) []domain.ReviewerSurface {
	if surfaces == nil {
		return []domain.ReviewerSurface{}
	}
	return surfaces
}

func reviewerSurfacePayload(surface domain.ReviewerSurface) *domain.ReviewerSurface {
	if surface.ReviewID == "" {
		return nil
	}
	return &surface
}

func (c *ReviewsController) submit(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/reviews/submit")
		return
	}
	var in SubmitReviewInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	reviews := make([]reviewsvc.SubmittedReview, 0, len(in.Reviews))
	if len(in.Reviews) > 0 {
		for _, item := range in.Reviews {
			reviews = append(reviews, reviewsvc.SubmittedReview{
				RunID:          item.RunID,
				Verdict:        domain.ReviewVerdict(item.Verdict),
				Body:           item.Body,
				GithubReviewID: item.GithubReviewID,
			})
		}
	} else {
		reviews = append(reviews, reviewsvc.SubmittedReview{
			RunID:          in.RunID,
			Verdict:        domain.ReviewVerdict(in.Verdict),
			Body:           in.Body,
			GithubReviewID: in.GithubReviewID,
		})
	}
	if agent, ok := scopedAgent(r); ok {
		// A reviewer records only its own review's runs; any other agent role
		// is already refused by the route policy.
		for i := range reviews {
			reviews[i].ReviewID = agent.ReviewID
		}
		if agent.ReviewID == "" {
			writeAgentScopeForbidden(w, r, "submit a review it was not launched for")
			return
		}
	}
	runs, err := c.Svc.SubmitMany(r.Context(), sessionID(r), reviews)
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	first := domain.ReviewRun{}
	if len(runs) > 0 {
		first = runs[0]
	}
	envelope.WriteJSON(w, http.StatusOK, ReviewRunResponse{Review: first, Reviews: runs})
}

func writeReviewError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, reviewsvc.ErrForeignReviewRun):
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "AGENT_SCOPE_FORBIDDEN", err.Error(), nil)
	case errors.Is(err, reviewsvc.ErrInvalid):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "REVIEW_INVALID", err.Error(), nil)
	case errors.Is(err, reviewsvc.ErrNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "REVIEW_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, reviewsvc.ErrPlanNotApproved):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PLAN_NOT_APPROVED", err.Error(), nil)
	case errors.Is(err, reviewsvc.ErrReviewAlreadyRunning):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "REVIEW_ALREADY_RUNNING", err.Error(), nil)
	case errors.Is(err, reviewsvc.ErrHeadAlreadyReviewed):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "REVIEW_HEAD_ALREADY_REVIEWED", err.Error(), nil)
	case errors.Is(err, reviewsvc.ErrPROwnedElsewhere):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "REVIEW_PR_OWNED_BY_OTHER_SESSION", err.Error(), nil)
	case errors.Is(err, reviewsvc.ErrAgentBinaryNotFound):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "REVIEWER_BINARY_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, ports.ErrChatAuthRequired):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "REVIEWER_AUTH_REQUIRED", "The reviewer agent is installed but not authenticated", nil)
	case errors.Is(err, reviewsvc.ErrConflict):
		// Any other conflict (for example the reviewer harness being updated) is
		// retryable and its message says why.
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "REVIEW_CONFLICT", err.Error(), nil)
	default:
		// The daemon API is loopback-only and token-authenticated, and a bare
		// "Review operation failed" left callers unable to tell a missing PR from
		// a provider outage, so the cause rides in the message and details.
		envelope.CaptureError(r, err)
		cause := truncateReviewCause(err.Error())
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "REVIEW_OPERATION_FAILED", "Review operation failed: "+cause, map[string]any{"cause": cause})
	}
}

const maxReviewCauseLen = 500

// truncateReviewCause bounds an error string echoed to the client.
func truncateReviewCause(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxReviewCauseLen {
		return s
	}
	return strings.ToValidUTF8(s[:maxReviewCauseLen], "") + "…"
}
