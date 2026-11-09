package controllers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/previewserver"
)

func (c *SessionsController) previewServerStatus(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil || c.PreviewServer == nil || c.Capabilities == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/sessions/{sessionId}/preview/server")
		return
	}
	if !c.authorizePreviewServer(w, r) {
		return
	}
	envelope.WriteJSON(w, http.StatusOK, previewServerStatusResponse(c.PreviewServer.Status(sessionID(r))))
}

func (c *SessionsController) startPreviewServer(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil || c.PreviewServer == nil || c.Capabilities == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/sessions/{sessionId}/preview/server")
		return
	}
	var in StartPreviewServerRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if !c.authorizePreviewServer(w, r) {
		return
	}
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	var projectEnv map[string]string
	if sess.ProjectID != "" && c.Projects != nil {
		project, err := c.Projects.Get(r.Context(), sess.ProjectID)
		if err != nil {
			envelope.WriteError(w, r, err)
			return
		}
		if project.Project != nil && project.Project.Config != nil {
			projectEnv = project.Project.Config.Env
		}
	}
	if c.refuseAgentPreviewStart(w, r) {
		return
	}
	previous := c.PreviewServer.Status(sessionID(r))
	status, err := c.PreviewServer.Start(
		r.Context(),
		sessionID(r),
		sess.Metadata.WorkspacePath,
		strings.TrimSpace(in.Configuration),
		projectEnv,
		c.previewFence(r.Context(), sessionID(r), sess.ProjectID),
	)
	if err != nil {
		currentStatus := c.PreviewServer.Status(sessionID(r))
		if previous.URL != "" &&
			(currentStatus.State != previewserver.StateReady || currentStatus.URL != previous.URL) {
			if current, getErr := c.Svc.Get(r.Context(), sessionID(r)); getErr == nil &&
				current.Metadata.PreviewURL == previous.URL {
				clearCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
				_, _ = c.Svc.SetPreview(clearCtx, sessionID(r), "")
				cancel()
			}
		}
		writePreviewServerError(w, r, err)
		return
	}
	if status.TargetKind == previewserver.TargetApp {
		if _, err := c.Svc.SetPreview(r.Context(), sessionID(r), status.URL); err != nil {
			_, _ = c.PreviewServer.Stop(context.Background(), sessionID(r))
			envelope.WriteError(w, r, err)
			return
		}
	}
	envelope.WriteJSON(w, http.StatusOK, previewServerStatusResponse(status))
}

func (c *SessionsController) stopPreviewServer(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil || c.PreviewServer == nil || c.Capabilities == nil {
		apispec.NotImplemented(w, r, http.MethodDelete, "/api/v1/sessions/{sessionId}/preview/server")
		return
	}
	if !c.authorizePreviewServer(w, r) {
		return
	}
	previous := c.PreviewServer.Status(sessionID(r))
	status, err := c.PreviewServer.Stop(r.Context(), sessionID(r))
	if err != nil {
		writePreviewServerError(w, r, err)
		return
	}
	current, getErr := c.Svc.Get(r.Context(), sessionID(r))
	if getErr != nil {
		envelope.WriteError(w, r, getErr)
		return
	}
	if previous.URL != "" && current.Metadata.PreviewURL == previous.URL {
		if _, err := c.Svc.SetPreview(r.Context(), sessionID(r), ""); err != nil {
			envelope.WriteError(w, r, err)
			return
		}
	}
	envelope.WriteJSON(w, http.StatusOK, previewServerStatusResponse(status))
}

func (c *SessionsController) authorizePreviewServer(w http.ResponseWriter, r *http.Request) bool {
	id := sessionID(r)
	sess, err := c.Svc.Get(r.Context(), id)
	if err != nil {
		envelope.WriteError(w, r, err)
		return false
	}
	if sess.IsTerminated {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SESSION_TERMINATED", "Session is terminated", nil)
		return false
	}
	if c.Capabilities.Valid(
		id,
		strings.TrimSpace(r.Header.Get(browserCapabilityHeader)),
		sess.Metadata.BrowserCapabilityVerifier,
	) {
		return true
	}
	if c.ShellPreviewCapabilities != nil {
		valid, err := c.ShellPreviewCapabilities.ValidPreviewCapability(
			r.Context(), id, strings.TrimSpace(r.Header.Get("X-AO-Preview-Capability")),
		)
		if err != nil {
			envelope.WriteError(w, r, err)
			return false
		}
		if valid {
			return true
		}
	}
	envelope.WriteAPIError(
		w,
		r,
		http.StatusForbidden,
		"forbidden",
		"PREVIEW_CAPABILITY_INVALID",
		"Preview capability is invalid",
		nil,
	)
	return false
}

func previewServerStatusResponse(status previewserver.Status) PreviewServerStatusResponse {
	logs := status.Logs
	if logs == nil {
		logs = []string{}
	}
	return PreviewServerStatusResponse{
		SessionID:     status.SessionID,
		State:         string(status.State),
		Configuration: status.Configuration,
		TargetKind:    string(status.TargetKind),
		URL:           status.URL,
		Port:          status.Port,
		StartedAt:     status.StartedAt,
		Error:         status.Error,
		Logs:          logs,
	}
}

func writePreviewServerError(w http.ResponseWriter, r *http.Request, err error) {
	var serviceErr previewserver.Error
	if !errors.As(err, &serviceErr) {
		envelope.WriteError(w, r, err)
		return
	}
	status := http.StatusUnprocessableEntity
	typeName := "unprocessable"
	switch serviceErr.Code {
	case "PREVIEW_CONFIG_NOT_FOUND", "PREVIEW_CONFIGURATION_NOT_FOUND":
		status = http.StatusNotFound
		typeName = "not_found"
	case "PREVIEW_CONFIGURATION_REQUIRED":
		status = http.StatusBadRequest
		typeName = "bad_request"
	case "PREVIEW_NOT_READY":
		status = http.StatusGatewayTimeout
		typeName = "timeout"
	case "PREVIEW_START_CANCELED":
		status = http.StatusRequestTimeout
		typeName = "timeout"
	case "PREVIEW_START_FAILED", "PREVIEW_EXITED", "PREVIEW_STOP_FAILED":
		status = http.StatusInternalServerError
		typeName = "internal_error"
	}
	envelope.WriteAPIError(w, r, status, typeName, serviceErr.Code, serviceErr.Message, nil)
}
