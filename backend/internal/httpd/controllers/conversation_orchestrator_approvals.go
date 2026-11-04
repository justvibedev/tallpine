package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

func (c *ConversationsController) pendingApprovals(r *http.Request, session domain.SessionID) ([]PendingApproval, error) {
	snap, err := c.Svc.Snapshot(r.Context(), session)
	if errors.Is(err, chatsvc.ErrNotChatMode) && c.Terminal != nil {
		pending, supported, terr := c.Terminal.PendingTerminalApprovals(r.Context(), session)
		if terr != nil {
			return nil, terr
		}
		if supported {
			return terminalPendingApprovals(pending), nil
		}
	}
	if err != nil {
		return nil, err
	}
	out := []PendingApproval{}
	for _, activity := range snap.Activities {
		if activity.Kind != domain.ActivityKindApproval || activity.Status != domain.ActivityStatusPending {
			continue
		}
		var detail struct {
			Decisions []PendingApprovalDecision `json:"decisions"`
		}
		_ = json.Unmarshal(activity.Detail, &detail)
		if detail.Decisions == nil {
			detail.Decisions = []PendingApprovalDecision{}
		}
		out = append(out, PendingApproval{
			RequestID: activity.RequestID,
			Summary:   activity.Summary,
			Detail:    json.RawMessage(activity.Detail),
			Decisions: detail.Decisions,
			CreatedAt: activity.CreatedAt,
		})
	}
	return out, nil
}

// pendingInputs lists the unanswered in-chat questions of a chat session.
func (c *ConversationsController) pendingInputs(r *http.Request, session domain.SessionID) ([]PendingInput, error) {
	snap, err := c.Svc.Snapshot(r.Context(), session)
	if errors.Is(err, chatsvc.ErrNotChatMode) {
		return []PendingInput{}, nil // a terminal session has no in-chat questions
	}
	if err != nil {
		return nil, err
	}
	out := []PendingInput{}
	for _, activity := range snap.Activities {
		if activity.Kind != domain.ActivityKindUserInput || activity.Status != domain.ActivityStatusPending {
			continue
		}
		out = append(out, PendingInput{
			RequestID: activity.RequestID, Summary: activity.Summary,
			Detail: json.RawMessage(activity.Detail), CreatedAt: activity.CreatedAt,
		})
	}
	return out, nil
}

// listApprovals answers GET .../conversation/approvals. The agent scope gate
// only lets an opted-in project's orchestrator reach it for its workers.
func (c *ConversationsController) listApprovals(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/sessions/{sessionId}/conversation/approvals")
		return
	}
	session := domain.SessionID(chi.URLParam(r, "sessionId"))
	approvals, err := c.pendingApprovals(r, session)
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	inputs, err := c.pendingInputs(r, session)
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, PendingApprovalsResponse{SessionID: string(session), Approvals: approvals, Inputs: inputs})
}

// ApprovalNotifier records a notification for the human; the notification
// manager satisfies it.
type ApprovalNotifier interface {
	Notify(ctx context.Context, intent ports.NotificationIntent) error
}

// orchestratorAnswer is what the human is told when an orchestrator answers.
type orchestratorAnswer struct {
	actor    string
	project  domain.ProjectID
	decision string
	kind     string
	summary  string
	command  string
	verb     string // overrides the allowed/rejected wording, for questions
}

// maxAuditText bounds the command and summary kept in the audit log and the
// notification: a Write or Edit approval's detail can hold a whole file.
const maxAuditText = 500

func truncateRunes(text string, limit int) string {
	text = strings.TrimSpace(text)
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}

// auditAnswer records an orchestrator's answer in the daemon log. Called only
// after the provider accepted it, so a failed resolve leaves no record.
func auditAnswer(session domain.SessionID, requestID, note string, a orchestratorAnswer) {
	if a.actor == "" {
		return
	}
	slog.Info("orchestrator answered a worker prompt",
		"audit", "orchestrator_approval", "actor", a.actor, "session", string(session),
		"project", string(a.project), "request", requestID, "decision", a.decision,
		"kind", a.kind, "summary", a.summary, "command", a.command, "note", note)
}

// notifyAnswer tells the human an orchestrator answered a worker's prompt. A
// failure is logged, not returned: the provider already has the answer and the
// audit row exists.
func (c *ConversationsController) notifyAnswer(r *http.Request, session domain.SessionID, requestID, note string, a orchestratorAnswer) {
	if c.Notifier == nil || a.actor == "" {
		return
	}
	verb := "allowed once"
	if strings.HasPrefix(a.kind, "reject") {
		verb = "rejected"
	}
	title := fmt.Sprintf("%s %s a prompt of %s", a.actor, verb, session)
	if a.verb != "" {
		title = fmt.Sprintf("%s %s of %s", a.actor, a.verb, session)
	}
	body := "Request: " + a.summary
	if a.command != "" {
		body += "\nCommand: " + a.command
	}
	if note != "" {
		body += "\nNote: " + note
	}
	if err := c.Notifier.Notify(r.Context(), ports.NotificationIntent{
		Type: domain.NotificationOrchestratorApproval, SessionID: session, ProjectID: a.project,
		SourceKey: "orchestrator-approval:" + string(session) + ":" + requestID,
		Title:     title, Body: body,
	}); err != nil {
		slog.Warn("could not notify the human of an orchestrator approval", "session", string(session), "request", requestID, "error", err)
	}
}

// approvalCommand is the exact command or tool input of an approval's detail.
func approvalCommand(detail json.RawMessage) string {
	var fields struct {
		RawCommand string `json:"rawCommand"`
		Command    string `json:"command"`
	}
	_ = json.Unmarshal(detail, &fields)
	if fields.RawCommand != "" {
		return fields.RawCommand
	}
	return fields.Command
}

// agentResolveActor is the audit actor stamped on an approval an orchestrator
// answered, or "" when the caller is the human (install token). It also
// narrows what an agent may choose: only a one-time allow or a reject, never a
// standing allow-always grant, and only for a request that is still pending.
// It writes the refusal and returns false when the agent may not proceed.
func (c *ConversationsController) agentResolveActor(
	w http.ResponseWriter, r *http.Request, session domain.SessionID, requestID, decisionID string,
) (orchestratorAnswer, bool) {
	agent, isAgent := requestscope.AgentFrom(r.Context())
	if !isAgent {
		return orchestratorAnswer{}, true
	}
	pending, err := c.pendingApprovals(r, session)
	if err != nil {
		writeConversationError(w, r, err)
		return orchestratorAnswer{}, false
	}
	for _, approval := range pending {
		if approval.RequestID != requestID {
			continue
		}
		for _, decision := range approval.Decisions {
			if decision.ID != decisionID {
				continue
			}
			kind := ports.ChatDecisionKind(decision.Kind)
			if class := humanOnlyClass(approval.Summary, approval.Detail); class != "" && kind == ports.ChatDecisionAllowOnce {
				envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "APPROVAL_HUMAN_ONLY",
					"only the user may allow this request ("+class+"); an orchestrator may reject it", nil)
				return orchestratorAnswer{}, false
			}
			switch kind {
			case ports.ChatDecisionAllowOnce, ports.ChatDecisionRejectOnce, ports.ChatDecisionRejectAlways:
				return orchestratorAnswer{
					actor: "orchestrator:" + string(agent.SessionID), project: agent.ProjectID,
					decision: decision.ID, kind: decision.Kind, summary: truncateRunes(approval.Summary, maxAuditText),
					command: truncateRunes(approvalCommand(approval.Detail), maxAuditText),
				}, true
			}
			envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "APPROVAL_DECISION_NOT_ALLOWED",
				"an orchestrator may allow a request once or reject it; a standing allow is for the user", nil)
			return orchestratorAnswer{}, false
		}
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_DECISION_UNKNOWN", "decisionId is not one of the request's decisions", nil)
		return orchestratorAnswer{}, false
	}
	envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found",
		"APPROVAL_NOT_PENDING", "no pending approval has that request id", nil)
	return orchestratorAnswer{}, false
}

// maxApprovalNote bounds the free text an orchestrator can leave on an approval.
const maxApprovalNote = 500

// approvalNote is the audit note to keep: only an agent's answer carries one.
func approvalNote(actor, note string) string {
	if actor == "" {
		return ""
	}
	note = strings.TrimSpace(note)
	if runes := []rune(note); len(runes) > maxApprovalNote {
		note = string(runes[:maxApprovalNote])
	}
	return note
}

// terminalPendingApprovals maps screen dialogs onto the chat approval shape so
// the scope gate, audit and notification treat both modes alike.
func terminalPendingApprovals(pending []ports.PendingTerminalApproval) []PendingApproval {
	out := make([]PendingApproval, 0, len(pending))
	for _, p := range pending {
		decisions := make([]PendingApprovalDecision, 0, len(p.Approval.Options))
		for _, o := range p.Approval.Options {
			decisions = append(decisions, PendingApprovalDecision{ID: o.ID, Label: o.Label, Kind: string(o.Kind)})
		}
		detail, _ := json.Marshal(map[string]string{"command": p.Approval.Command})
		out = append(out, PendingApproval{
			RequestID: p.RequestID, Summary: p.Approval.Summary, Detail: detail,
			Decisions: decisions, CreatedAt: time.Now().UTC(),
		})
	}
	return out
}
