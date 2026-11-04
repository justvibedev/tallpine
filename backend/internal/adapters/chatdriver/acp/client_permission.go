package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (c *conversation) RequestPermission(
	ctx context.Context,
	params acpsdk.RequestPermissionRequest,
) (acpsdk.RequestPermissionResponse, error) {
	if len(params.Options) == 0 {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeCancelled()}, nil
	}
	c.mu.Lock()
	policy, mode := c.permissionFor, c.permissionMode
	c.mu.Unlock()
	if policy != nil {
		if selected, handled := policy(mode, params); handled {
			for _, option := range params.Options {
				if option.OptionId == selected {
					return acpsdk.RequestPermissionResponse{
						Outcome: acpsdk.NewRequestPermissionOutcomeSelected(selected),
					}, nil
				}
			}
			return acpsdk.RequestPermissionResponse{}, fmt.Errorf(
				"provider permission policy selected unoffered option %q", selected)
		}
	}
	decisions := make([]ports.ChatDecisionOption, 0, len(params.Options))
	for _, option := range params.Options {
		id := string(option.OptionId)
		raw, _ := json.Marshal(option)
		decisions = append(decisions, ports.ChatDecisionOption{
			ID: id, Label: option.Name, Kind: ports.ChatDecisionKind(option.Kind), Raw: raw,
		})
	}
	// A bare request (dsh sends only the toolCallId) is described by the
	// tool_call update the agent streamed just before asking.
	tool := c.approvalTool(params.ToolCall)
	summary := "Permission required"
	if tool.Title != nil && strings.TrimSpace(*tool.Title) != "" {
		summary = *tool.Title
	}
	selected, err := c.requestApproval(ctx, stableACPRequestID(params.Meta), ClientApprovalRequest{
		Summary: summary, ActivityKind: activityKindFromTool(pointerValue(tool.Kind)),
		Detail:    approvalToolDetail(tool, activityKindFromTool(pointerValue(tool.Kind)), airPermissionReason(params.Meta)),
		Decisions: decisions,
	})
	if err != nil || selected == "" {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeCancelled()}, err
	}
	return acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.NewRequestPermissionOutcomeSelected(acpsdk.PermissionOptionId(selected)),
	}, nil
}

// RequestApproval parks a provider extension on Tallpine's durable approval flow.
func (c *conversation) RequestApproval(
	ctx context.Context,
	params ClientApprovalRequest,
) (string, error) {
	return c.requestApproval(ctx, "", params)
}

func (c *conversation) requestApproval(
	ctx context.Context,
	requestID string,
	params ClientApprovalRequest,
) (string, error) {
	if len(params.Decisions) == 0 {
		return "", nil
	}
	if requestID == "" {
		requestID = uuid.NewString()
	}
	options := make(map[string]json.RawMessage, len(params.Decisions))
	for _, option := range params.Decisions {
		options[option.ID] = append(json.RawMessage(nil), option.Raw...)
	}
	request := &parkedPermission{
		options: options, result: make(chan string, 1), ready: make(chan struct{}),
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return "", nil
	}
	accepted, hasAccepted := c.accepted[requestID]
	if hasAccepted && accepted.Kind == persistentInteractionApproval {
		delete(c.accepted, requestID)
	} else {
		c.pending[requestID] = request
	}
	turnID := c.activeTurn
	c.mu.Unlock()
	c.emit(ports.ChatEvent{
		Kind:           ports.ChatEventApprovalRequested,
		ProviderTurnID: turnID,
		ProviderItemID: requestID,
		ActivityKind:   params.ActivityKind,
		ActivityStatus: domain.ActivityStatusPending,
		Summary:        params.Summary,
		Detail:         params.Detail,
		RequestID:      requestID,
		Decisions:      params.Decisions,
	})
	close(request.ready)
	if hasAccepted && accepted.Kind == persistentInteractionApproval {
		c.emit(persistentInteractionEvent(accepted))
		request.result <- accepted.Decision.ID
	}

	timer := timeAfter(approvalWait)
	select {
	case selected := <-request.result:
		return selected, nil
	case <-ctx.Done():
		c.discardPermission(requestID)
		if !c.providerDetaching() {
			c.emit(ports.ChatEvent{Kind: ports.ChatEventApprovalResolved, RequestID: requestID})
		}
		return "", nil
	case <-timer:
		c.discardPermission(requestID)
		c.emit(ports.ChatEvent{Kind: ports.ChatEventApprovalResolved, RequestID: requestID})
		return "", nil
	}
}

const (
	maxApprovalCommandBytes = 8000
	maxApprovalReasonBytes  = 2000
)

// truncateBytes caps s at limit bytes, ellipsis included, cutting on a rune
// boundary. The caps bound the stored detail, so they count bytes, not runes.
func truncateBytes(s string, limit int) string {
	const ellipsis = "…"
	if len(s) <= limit {
		return s
	}
	cut := limit - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ellipsis
}

// approvalTool fills the fields a permission request left out from the tool
// call the agent already announced under the same id.
func (c *conversation) approvalTool(tool acpsdk.ToolCallUpdate) acpsdk.ToolCallUpdate {
	c.mu.Lock()
	known := c.tools[string(tool.ToolCallId)]
	c.mu.Unlock()
	if known == nil {
		return tool
	}
	if tool.Title == nil && known.title != "" {
		title := known.title
		tool.Title = &title
	}
	if tool.Kind == nil && known.kind != "" {
		kind := known.kind
		tool.Kind = &kind
	}
	if tool.RawInput == nil {
		tool.RawInput = known.rawInput
	}
	if len(tool.Content) == 0 {
		tool.Content = known.content
	}
	if tool.Meta == nil {
		tool.Meta = known.meta
	}
	return tool
}

// approvalReason is the agent's own explanation for asking: an explicit reason
// in the tool input or _meta, else the first text block of the tool content.
func approvalReason(tool acpsdk.ToolCallUpdate) string {
	if input, ok := tool.RawInput.(map[string]any); ok {
		for _, key := range []string{"reason", "justification"} {
			if text, ok := input[key].(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	if text, ok := tool.Meta["reason"].(string); ok && strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text)
	}
	for _, item := range tool.Content {
		if item.Content != nil && item.Content.Content.Text != nil {
			if text := strings.TrimSpace(item.Content.Content.Text.Text); text != "" {
				return text
			}
		}
	}
	return ""
}

// airPermissionReason is the provider's own explanation for asking, carried in
// the request _meta (jetbrains.air.permission.description) by claude-agent-acp
// as "Reason: <decisionReason>". For a session in bypass-permissions it is the
// only way to tell why a bypass-immune request still prompted.
func airPermissionReason(meta map[string]any) string {
	air := nestedMap(nestedMap(meta, "jetbrains"), "air")
	permission := nestedMap(air, "permission")
	text, _ := permission["description"].(string)
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "Reason:"))
}

func approvalToolDetail(tool acpsdk.ToolCallUpdate, activityKind domain.ActivityKind, providerReason string) []byte {
	detail := map[string]any{
		"protocol": "acp", "toolKind": pointerValue(tool.Kind),
		"subjectKind": string(activityKind),
	}
	if tool.RawInput != nil {
		detail["input"] = cappedPayload(tool.RawInput)
	}
	if command := rawCommandFromInput(tool.RawInput); command != "" {
		command = truncateBytes(command, maxApprovalCommandBytes)
		detail["command"] = command
		detail["rawCommand"] = command
	}
	reason := providerReason
	if reason == "" {
		reason = approvalReason(tool)
	}
	if reason != "" {
		detail["reason"] = truncateBytes(reason, maxApprovalReasonBytes)
	}
	if claude := nestedMap(tool.Meta, "claudeCode"); claude != nil {
		copyDetail(detail, claude, "toolName", "providerToolName")
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return nil
	}
	return encoded
}

func (c *conversation) providerDetaching() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed && c.proc != nil && c.proc.terminate != nil && c.conn != nil {
		select {
		case <-c.conn.Done():
			return true
		default:
		}
	}
	return c.detaching
}

func (c *conversation) discardPermission(requestID string) {
	c.mu.Lock()
	delete(c.pending, requestID)
	c.mu.Unlock()
}
