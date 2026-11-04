package controllers

import (
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

func (c *SessionsController) rename(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/sessions/{sessionId}")
		return
	}
	var in RenameSessionRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	displayName := strings.TrimSpace(in.DisplayName)
	if displayName == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "DISPLAY_NAME_REQUIRED", "displayName is required", nil)
		return
	}
	if utf8.RuneCountInString(displayName) > maxDisplayNameLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "DISPLAY_NAME_TOO_LONG", fmt.Sprintf("displayName must be %d characters or fewer", maxDisplayNameLen), nil)
		return
	}
	if err := c.Svc.Rename(r.Context(), sessionID(r), displayName); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, RenameSessionResponse{OK: true, SessionID: sessionID(r), DisplayName: displayName})
}

func (c *SessionsController) setMergePolicy(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/sessions/{sessionId}/merge-policy")
		return
	}
	var in SetSessionMergePolicyRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	sess, err := c.Svc.SetTerminateOnPRMerge(r.Context(), sessionID(r), in.TerminateOnPRMerge)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SetSessionMergePolicyResponse{
		OK:                 true,
		SessionID:          sessionID(r),
		TerminateOnPRMerge: in.TerminateOnPRMerge,
		Session:            c.view(r.Context(), r, sess),
	})
}

func (c *SessionsController) setAutoInjectReviewPolicy(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/sessions/{sessionId}/auto-inject-review")
		return
	}
	var in SetSessionAutoInjectReviewRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	sess, err := c.Svc.SetAutoInjectReview(r.Context(), sessionID(r), in.AutoInjectReview)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SetSessionAutoInjectReviewResponse{
		OK:               true,
		SessionID:        sessionID(r),
		AutoInjectReview: in.AutoInjectReview,
		Session:          c.view(r.Context(), r, sess),
	})
}

func (c *SessionsController) setAutoInjectCIPolicy(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/sessions/{sessionId}/auto-inject-ci")
		return
	}
	// Decode through a pointer so an omitted required field remains distinct
	// from the valid explicit value {"autoInjectCI":false}. The public request
	// DTO stays non-pointer so the generated OpenAPI schema remains a required,
	// non-nullable boolean.
	var in struct {
		AutoInjectCI *bool `json:"autoInjectCI"`
	}
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if in.AutoInjectCI == nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "AUTO_INJECT_CI_REQUIRED", "autoInjectCI is required", nil)
		return
	}
	autoInjectCI := *in.AutoInjectCI
	sess, err := c.Svc.SetAutoInjectCI(r.Context(), sessionID(r), autoInjectCI)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SetSessionAutoInjectCIResponse{
		OK:           true,
		SessionID:    sessionID(r),
		AutoInjectCI: autoInjectCI,
		Session:      c.view(r.Context(), r, sess),
	})
}

func (c *SessionsController) setReviewer(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/sessions/{sessionId}/reviewer")
		return
	}
	var in SetSessionReviewerRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	sess, err := c.Svc.SetReviewerHarness(r.Context(), sessionID(r), in.Harness, in.AgentConfig)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: c.view(r.Context(), r, sess)})
}

func (c *SessionsController) setAutoReview(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/sessions/{sessionId}/auto-review")
		return
	}
	var in SetSessionAutoReviewRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if !in.enabledPresent {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "AUTO_REVIEW_ENABLED_REQUIRED", "enabled is required", nil)
		return
	}
	sess, err := c.Svc.SetAutoReview(r.Context(), sessionID(r), in.Enabled)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: c.view(r.Context(), r, sess)})
}
