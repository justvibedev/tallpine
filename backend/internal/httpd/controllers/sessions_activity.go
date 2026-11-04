package controllers

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/ports"
	usagesvc "tallpine.eu/tallpine/backend/internal/service/usage"
)

// activity records an agent activity-state signal reported by an agent hook
// (via `tallpine hooks <agent> <event>`). It funnels through the single
// lifecycle.Manager so the reaper and hooks never race on the session's
// activity/termination columns.
func (c *SessionsController) activity(w http.ResponseWriter, r *http.Request) {
	if c.Activity == nil && c.Usage == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/activity")
		return
	}
	var in SetActivityRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	state := domain.ActivityState(in.State)
	if state != "" {
		switch state {
		case domain.ActivityActive, domain.ActivityIdle, domain.ActivityWaitingInput, domain.ActivityBlocked, domain.ActivityExited:
		default:
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_ACTIVITY_STATE", "Unknown activity state", nil)
			return
		}
	}
	agentSessionID := capActivityMeta(domain.SanitizeControlChars(strings.TrimSpace(in.AgentSessionID)))
	checkpointOrigin := domain.ConversationCheckpointOrigin(strings.TrimSpace(string(in.ConversationCheckpointOrigin)))
	if !checkpointOrigin.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_CONVERSATION_CHECKPOINT_ORIGIN", "Conversation checkpoint origin must be human or coordination", nil)
		return
	}
	if state == "" && agentSessionID == "" && in.Usage == nil && in.SubagentID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "ACTIVITY_OR_SESSION_ID_REQUIRED", "Activity state, agent session ID, or subagent ID is required", nil)
		return
	}
	// The correlation fields ride the same lenient decode: absent on old CLIs.
	// They are externally-supplied strings headed for logs and in-memory maps,
	// so sanitize control chars and cap their length (a truncated id could
	// never match its pre/post counterpart, so overlong values are dropped by
	// the CLI; the cap here is defense against non-Tallpine callers).
	sig := ports.ActivitySignal{
		Timestamp:                    in.ObservedAt,
		Valid:                        state != "",
		State:                        state,
		Event:                        capActivityMeta(domain.SanitizeControlChars(in.Event)),
		ToolName:                     capActivityMeta(domain.SanitizeControlChars(in.ToolName)),
		ToolUseID:                    capActivityMeta(domain.SanitizeControlChars(in.ToolUseID)),
		SubagentID:                   capActivityMeta(domain.SanitizeControlChars(in.SubagentID)),
		AgentSessionID:               agentSessionID,
		LatestUserPrompt:             capActivityText(domain.SanitizeControlChars(strings.TrimSpace(in.LatestUserPrompt)), 16<<10),
		LatestAssistantUpdate:        capActivityText(domain.SanitizeControlChars(strings.TrimSpace(in.LatestAssistantUpdate)), 16<<10),
		FinalAssistantMessage:        capActivityText(domain.SanitizeControlChars(strings.TrimSpace(in.FinalAssistantMessage)), 16<<10),
		TurnUserRequest:              capActivityText(domain.SanitizeControlChars(strings.TrimSpace(in.TurnUserRequest)), 16<<10),
		ExpectedHarness:              domain.AgentHarness(capActivityMeta(domain.SanitizeControlChars(strings.TrimSpace(in.Harness)))),
		ConversationCheckpointOrigin: checkpointOrigin,
		CoordinationID:               capActivityMeta(domain.SanitizeControlChars(strings.TrimSpace(in.CoordinationID))),
		ProviderTurnID:               capActivityMeta(domain.SanitizeControlChars(strings.TrimSpace(in.ProviderTurnID))),
		SubmissionID:                 capActivityMeta(domain.SanitizeControlChars(strings.TrimSpace(in.SubmissionID))),
		TranscriptPath:               capActivityText(domain.SanitizeControlChars(strings.TrimSpace(in.TranscriptPath)), 4096),
		LaunchID:                     capActivityMeta(domain.SanitizeControlChars(strings.TrimSpace(in.LaunchID))),
		NativeEntrypoint:             capActivityMeta(domain.SanitizeControlChars(strings.TrimSpace(in.Entrypoint))),
		ProcessID:                    max(in.ProcessID, 0),
	}
	if in.RunningSubagentIDs != nil && len(*in.RunningSubagentIDs) <= 128 {
		running := make([]string, 0, len(*in.RunningSubagentIDs))
		for _, id := range *in.RunningSubagentIDs {
			clean := capActivityMeta(domain.SanitizeControlChars(strings.TrimSpace(id)))
			if clean == "" {
				running = nil
				break
			}
			running = append(running, clean)
		}
		if running != nil {
			sig.RunningSubagentIDs = &running
		}
	}
	var activityErr error
	if c.Activity != nil && (sig.Valid || sig.AgentSessionID != "" || sig.SubagentID != "") {
		activityErr = c.Activity.ApplyActivitySignal(r.Context(), sessionID(r), sig)
		if err := activityErr; err != nil && !errors.Is(err, ports.ErrActivityProjectionContention) {
			if errors.Is(err, ports.ErrSessionNotFound) {
				envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", "Unknown session", nil)
				return
			}
			envelope.WriteError(w, r, err)
			return
		}
	}
	if c.Usage != nil {
		usageSignal := usagesvc.HookSignal{
			Event:           sig.Event,
			LaunchID:        sig.LaunchID,
			NativeSessionID: agentSessionID,
		}
		if in.Usage != nil {
			usageSignal.Harness = domain.AgentHarness(capActivityMeta(domain.SanitizeControlChars(strings.TrimSpace(string(in.Usage.Harness)))))
			usageSignal.ProviderHint = capActivityMeta(domain.SanitizeControlChars(strings.TrimSpace(in.Usage.ProviderID)))
			usageSignal.TranscriptPath = capUsagePath(domain.SanitizeControlChars(strings.TrimSpace(in.Usage.TranscriptPath)))
			usageSignal.ModelID = capActivityMeta(domain.SanitizeControlChars(strings.TrimSpace(in.Usage.ModelID)))
			usageSignal.SubagentID = capActivityMeta(domain.SanitizeControlChars(strings.TrimSpace(in.Usage.SubagentID)))
			usageSignal.SubagentTranscriptPath = capUsagePath(domain.SanitizeControlChars(strings.TrimSpace(in.Usage.SubagentTranscriptPath)))
		}
		if err := c.Usage.RecordHook(r.Context(), sessionID(r), usageSignal); err != nil {
			if errors.Is(err, usagesvc.ErrUsageSessionNotFound) {
				envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", "Unknown session", nil)
				return
			}
			slog.Default().Warn(
				"usage hook processing failed",
				"session", sessionID(r),
				"event", sig.Event,
				"err", err,
			)
		}
	}
	if activityErr != nil {
		// The projection never committed, so the hook can retry the same payload.
		// Usage observation is independent and must still run on contention.
		w.Header().Set("Retry-After", "1")
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "ACTIVITY_PROJECTION_BUSY", "Concurrent session updates prevented this activity signal from committing; retry the hook", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SetActivityResponse{OK: true, SessionID: sessionID(r), State: in.State})
}

// capActivityMeta bounds an optional activity correlation string; overlong
// values are dropped, not truncated (see the comment at its call site).
func capActivityMeta(v string) string {
	const maxLen = 256
	if len(v) > maxLen {
		return ""
	}
	return v
}

func capUsagePath(v string) string {
	const maxLen = 4096
	if len(v) > maxLen {
		return ""
	}
	return v
}

func capActivityText(v string, maxLen int) string {
	if maxLen <= 0 || len(v) <= maxLen {
		return v
	}
	const marker = "\n[... truncated by Tallpine ...]\n"
	budget := maxLen - len(marker)
	if budget <= 0 {
		return ""
	}
	head := budget / 2
	tail := budget - head
	return strings.ToValidUTF8(string([]byte(v)[:head])+marker+string([]byte(v)[len(v)-tail:]), "?")
}
