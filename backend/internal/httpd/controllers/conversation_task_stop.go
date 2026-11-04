package controllers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/bgtasks"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

const stopBackgroundTaskPath = "/api/v1/sessions/{sessionId}/background-tasks/stop"

// StopBackgroundTaskRequest names the background task to stop.
type StopBackgroundTaskRequest struct {
	// TaskID is the harness task id, as listed in the session's backgroundTasks.
	TaskID string `json:"taskId"`
}

// StopBackgroundTaskResponse reports that the harness stopped the task.
type StopBackgroundTaskResponse struct {
	Stopped bool `json:"stopped"`
}

type backgroundTaskStopper interface {
	StopAsyncTask(context.Context, domain.SessionID, string) (bool, error)
}

// BackgroundTaskLister lists a session's running background tasks.
type BackgroundTaskLister interface {
	Running(domain.SessionID) []bgtasks.Info
	// ExpectStop and CancelStop keep a deliberate stop from waking the agent.
	ExpectStop(domain.SessionID, string)
	CancelStop(domain.SessionID, string)
}

// stopBackgroundTask stops one harness background task of a session. Who may
// call it is decided by the agent scope rules (httpd/agent_scope.go); this
// handler only knows the session.
func (c *ConversationsController) stopBackgroundTask(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(backgroundTaskStopper)
	if !ok || c.BackgroundTasks == nil {
		apispec.NotImplemented(w, r, "POST", stopBackgroundTaskPath)
		return
	}
	var req StopBackgroundTaskRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"BACKGROUND_TASK_ID_REQUIRED", "taskId is required", nil)
		return
	}
	sessionID := domain.SessionID(chi.URLParam(r, "sessionId"))
	known := false
	for _, task := range c.BackgroundTasks.Running(sessionID) {
		if task.ID == taskID {
			known = true
			break
		}
	}
	if !known {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found",
			"BACKGROUND_TASK_NOT_FOUND", "no running background task "+taskID+" in this session", nil)
		return
	}
	// Before the request: the harness can report the end before it returns.
	c.BackgroundTasks.ExpectStop(sessionID, taskID)
	stopped, err := svc.StopAsyncTask(r.Context(), sessionID, taskID)
	if err != nil || !stopped {
		c.BackgroundTasks.CancelStop(sessionID, taskID)
	}
	switch {
	case errors.Is(err, chatsvc.ErrNoController):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_NO_CONTROLLER", "the session's agent is not running", nil)
	case errors.Is(err, chatsvc.ErrAsyncTaskStopUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"BACKGROUND_TASK_STOP_UNSUPPORTED", err.Error(), nil)
	case err != nil:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal",
			"BACKGROUND_TASK_STOP_FAILED", err.Error(), nil)
	case !stopped:
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"BACKGROUND_TASK_NOT_STOPPED", "the agent did not stop the task; it may have just ended", nil)
	default:
		envelope.WriteJSON(w, http.StatusOK, StopBackgroundTaskResponse{Stopped: true})
	}
}
