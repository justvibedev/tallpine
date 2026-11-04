package controllers

import (
	"encoding/json"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// conversationSnapshotResponse maps the service snapshot onto the wire shape.
// Items arrive already ordered by sequence, so nothing is re-sorted here.
func conversationSnapshotResponse(s chatsvc.Snapshot) ConversationSnapshotResponse {
	out := ConversationSnapshotResponse{
		ContinueTurnID:                   chatsvc.ContinuableTurnID(s.Turns),
		ConversationID:                   s.Conversation.ID,
		ActiveBranchID:                   s.Conversation.ActiveBranchID,
		BranchedFromEarlierMessage:       s.BranchedFromEarlierMessage,
		SessionID:                        string(s.SessionID),
		Harness:                          string(s.Harness),
		Mode:                             string(s.Mode),
		Controller:                       string(s.Controller),
		LatestSequence:                   s.Conversation.LatestSequence,
		OldestSequence:                   s.OldestSequence,
		HasMoreBefore:                    s.HasMoreBefore,
		NativeForkAvailableAfterSequence: s.NativeForkAvailableAfterSequence,
		Turns:                            make([]ConversationTurnResponse, 0, len(s.Turns)),
		Messages:                         make([]ConversationMessageResponse, 0, len(s.Messages)),
		Activities:                       make([]ConversationActivityResponse, 0, len(s.Activities)),
		BranchPoints:                     make([]ConversationBranchPointResponse, 0, len(s.BranchPoints)),
		BranchMaterialization:            branchMaterializationPayload(s.ActiveBranch),
		Settings:                         turnSettingsPayload(s.Conversation.Settings),
		Usage:                            usagePayload(s.Usage),
		RateLimits:                       rateLimitsPayload(s.RateLimits),
		CompactedAt:                      optionalTimestamp(s.Conversation.CompactedAt),
		Title:                            s.Conversation.ProviderTitle,
		ModelReroute:                     modelReroutePayload(s.Conversation.ModelReroute),
		Account:                          accountPayload(s.Conversation.Account),
		ThreadState:                      threadStatePayload(s.Conversation.ThreadState),
		MCPServers:                       mcpServersPayload(s.Conversation.MCPServers),
		Capabilities:                     capabilityNames(s.Capabilities),
	}

	for _, turn := range s.Turns {
		out.Turns = append(out.Turns, ConversationTurnResponse{
			ID:              turn.ID,
			State:           string(turn.State),
			ProviderTurnID:  turn.ProviderTurnID,
			RetryOfTurnID:   turn.RetryOfTurnID,
			HasRetryAttempt: turn.HasRetryAttempt,
			HasHumanPrompt:  turn.HasHumanPrompt,
			ErrorMessage:    turn.ErrorMessage,
			UsageLimit:      usageLimitPayload(turn.UsageLimit),
			RequestedAt:     turn.RequestedAt.UTC().Format(time.RFC3339),
			StartedAt:       optionalTimestamp(turn.StartedAt),
			CompletedAt:     optionalTimestamp(turn.CompletedAt),
			Diff:            turnDiffPayload(turn.Diff),
			Plan:            turnPlanPayload(turn.Plan),
			RolledBack:      turn.RolledBackAt != nil,
		})
	}

	for _, msg := range s.Messages {
		message := ConversationMessageResponse{
			Kind:              "message",
			ID:                msg.ID,
			TurnID:            msg.TurnID,
			Sequence:          msg.Sequence,
			Revision:          msg.Revision,
			Role:              string(msg.Role),
			Origin:            string(msg.Origin),
			Text:              msg.Text,
			SenderSessionID:   msg.SenderSessionID,
			SenderProjectID:   msg.SenderProjectID,
			SenderDisplayName: msg.SenderDisplayName,
			ClientMessageID:   msg.ClientMessageID,
			Streaming:         msg.Streaming,
			CreatedAt:         msg.CreatedAt.UTC().Format(time.RFC3339),
			Continuation:      msg.Continuation,
		}
		message.Content, message.EditAvailable = conversationContentSummary(msg)
		message.EditAvailable = message.EditAvailable && msg.Sequence > s.EditFloorSequence
		out.Messages = append(out.Messages, message)
	}
	for _, point := range s.BranchPoints {
		out.BranchPoints = append(out.BranchPoints, ConversationBranchPointResponse{
			TurnID: point.TurnID, Position: point.Position, Total: point.Total,
			PreviousBranchID: point.PreviousBranchID, NextBranchID: point.NextBranchID,
		})
	}

	for _, activity := range s.Activities {
		out.Activities = append(out.Activities, ConversationActivityResponse{
			Kind:           "activity",
			ID:             activity.ID,
			TurnID:         activity.TurnID,
			Sequence:       activity.Sequence,
			Revision:       activity.Revision,
			ActivityKind:   string(activity.Kind),
			Status:         string(activity.Status),
			Summary:        activity.Summary,
			Detail:         activityDetailPayload(activity),
			RequestID:      activity.RequestID,
			ProviderItemID: activity.ProviderItemID,
			CreatedAt:      activity.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func usageLimitPayload(limit *domain.ConversationUsageLimit) *ConversationUsageLimitResponse {
	if limit == nil {
		return nil
	}
	return &ConversationUsageLimitResponse{ResetsAt: optionalTimestamp(limit.ResetsAt), AutoResume: limit.AutoResume, ResumeError: limit.ResumeError}
}

func branchMaterializationPayload(branch domain.ConversationBranch) *ConversationBranchMaterializationResponse {
	if branch.ID == "" || branch.Strategy == "" {
		return nil
	}
	return &ConversationBranchMaterializationResponse{
		Strategy:        string(branch.Strategy),
		ReplayTruncated: branch.ReplayTruncated,
	}
}

func conversationContentSummary(msg domain.ConversationMessage) ([]ConversationContentSummaryResponse, bool) {
	if msg.Role != domain.MessageRoleUser || msg.Origin != domain.MessageOriginHuman {
		return nil, false
	}
	if msg.DeliveryContentJSON == "" {
		return nil, true
	}
	var content []ports.ChatContent
	if err := json.Unmarshal([]byte(msg.DeliveryContentJSON), &content); err != nil {
		return nil, false
	}
	summaries := make([]ConversationContentSummaryResponse, 0, len(content))
	for _, block := range content {
		// Tallpine's reconstructed-history seed is provider context, not content the
		// person attached. Keeping it out of this public summary prevents it from
		// appearing as a user resource when the edited message is rendered again.
		if block.Type == "text" || ports.IsInternalReplayContent(block) {
			continue
		}
		name := block.Name
		if name == "" && block.Type != "image" && block.Type != "resource" && block.Type != "resource_link" {
			name = block.Type
		}
		summary := ConversationContentSummaryResponse{Type: block.Type, MIMEType: block.MIMEType, Name: name}
		if block.Type == "excerpt" {
			// Excerpts written by current versions carry verified structured
			// context. Older durable messages only have the excerpt resource URI
			// and selected text; keep those navigable without exposing the
			// internal URI in the public content summary.
			summary.Name = "Chat excerpt"
			if block.Excerpt != nil {
				summary.Text = block.Excerpt.SelectedText
				summary.SourceConversationID = block.Excerpt.Reference.ConversationID
				summary.SourceMessageID = block.Excerpt.Reference.MessageID
				summary.SourceRevision = block.Excerpt.Reference.Revision
			} else {
				summary.Text = block.Text
				if strings.HasPrefix(block.URI, ports.ChatExcerptResourceURIPrefix) {
					summary.SourceMessageID = strings.TrimPrefix(block.URI, ports.ChatExcerptResourceURIPrefix)
				}
			}
		} else {
			summary.URI = block.URI
		}
		summaries = append(summaries, summary)
	}
	return summaries, true
}

// usagePayload maps the conversation's token position onto the wire shape. A nil
// snapshot value stays nil: the field being absent is how a client learns the
// provider has not reported yet, which is not the same as a conversation that has
// used nothing.
func usagePayload(usage *domain.ConversationUsage) *ConversationUsagePayload {
	if usage == nil {
		return nil
	}
	return &ConversationUsagePayload{
		ContextUsed:   usage.ContextUsed,
		ContextWindow: usage.ContextWindow,
		InputTokens:   usage.InputTokens,
		OutputTokens:  usage.OutputTokens,
		CachedTokens:  usage.CachedTokens,
		TotalTokens:   usage.TotalTokens,
		Cost:          usage.Cost,
		Currency:      usage.Currency,
	}
}

// rateLimitsPayload maps the account's quota position onto the wire shape.
func rateLimitsPayload(limits *domain.ConversationRateLimits) *ConversationRateLimitsPayload {
	if limits == nil {
		return nil
	}
	return &ConversationRateLimitsPayload{
		PrimaryUsedPercent:       limits.PrimaryUsedPercent,
		SecondaryUsedPercent:     limits.SecondaryUsedPercent,
		PrimaryResetsInSeconds:   limits.PrimaryResetsInSeconds,
		SecondaryResetsInSeconds: limits.SecondaryResetsInSeconds,
		PlanLabel:                limits.PlanLabel,
	}
}

// turnDiffPayload maps a turn's changed-file summary onto the wire shape.
func turnDiffPayload(diff *domain.ConversationTurnDiff) *ConversationTurnDiffResponse {
	if diff == nil {
		return nil
	}
	out := &ConversationTurnDiffResponse{
		Truncated: diff.Truncated,
		Files:     make([]ConversationDiffFileResponse, 0, len(diff.Files)),
	}
	for _, file := range diff.Files {
		out.Files = append(out.Files, ConversationDiffFileResponse{
			Path:      file.Path,
			Additions: file.Additions,
			Deletions: file.Deletions,
			Status:    file.Status,
			OldPath:   file.OldPath,
		})
	}
	return out
}

// turnPlanPayload maps a turn's plan onto the wire shape. A nil plan stays nil: the
// agent made none, which is not the same as making an empty one.
func turnPlanPayload(plan *domain.ConversationPlan) *ConversationPlanResponse {
	if plan == nil {
		return nil
	}
	out := &ConversationPlanResponse{
		Explanation: plan.Explanation,
		Steps:       make([]ConversationPlanStepResponse, 0, len(plan.Steps)),
		SpansTurns:  plan.SpansTurns,
	}
	for _, step := range plan.Steps {
		out.Steps = append(out.Steps, ConversationPlanStepResponse{
			Text:   step.Text,
			Status: string(step.Status),
		})
	}
	return out
}

// modelReroutePayload maps a model substitution onto the wire shape.
//
// Absent means the provider never swapped the model, which is why it is not folded
// into the settings payload: settings say what Tallpine asked for, and this says what
// answered. Collapsing them would leave a client unable to tell "I chose this" from
// "this replied".
func modelReroutePayload(reroute *domain.ConversationModelReroute) *ConversationModelReroutePayload {
	if reroute == nil {
		return nil
	}
	return &ConversationModelReroutePayload{
		FromModel:      reroute.FromModel,
		ToModel:        reroute.ToModel,
		Reason:         reroute.Reason,
		ProviderTurnID: reroute.ProviderTurnID,
		At:             reroute.At.UTC().Format(time.RFC3339),
	}
}

// accountPayload maps the provider account onto the wire shape.
func accountPayload(account *domain.ConversationAccount) *ConversationAccountPayload {
	if account == nil {
		return nil
	}
	state := account.AuthenticationState
	if state == "" {
		state = "unknown"
		if account.ReauthRequiredAt != nil {
			state = "required"
		}
	}
	return &ConversationAccountPayload{
		AuthenticationState:   state,
		AuthVerifiedAt:        optionalTimestamp(account.AuthVerifiedAt),
		LastAuthFailureAt:     optionalTimestamp(account.LastAuthFailureAt),
		LastAuthFailureReason: account.LastAuthFailureReason,
		AuthFailureID:         account.AuthFailureID,
		AuthMode:              account.AuthMode,
		PlanLabel:             account.PlanLabel,
		ReauthRequiredAt:      optionalTimestamp(account.ReauthRequiredAt),
		ReauthReason:          account.ReauthReason,
	}
}

// threadStatePayload maps the provider's thread lifecycle onto the wire shape.
func threadStatePayload(state *domain.ConversationThreadState) *ConversationThreadStatePayload {
	if state == nil {
		return nil
	}
	payload := &ConversationThreadStatePayload{
		Status:     string(state.Status),
		WaitingOn:  state.WaitingOn,
		ArchivedAt: optionalTimestamp(state.ArchivedAt),
		ClosedAt:   optionalTimestamp(state.ClosedAt),
	}
	if c := state.Compaction; c != nil {
		payload.Compaction = &ConversationCompactionPayload{
			Status: string(c.Status), Trigger: c.Trigger,
			PreTokens: c.PreTokens, PostTokens: c.PostTokens,
			At: c.At.UTC().Format(time.RFC3339),
		}
	}
	return payload
}

// mcpServersPayload maps the tool servers onto the wire shape, preserving the order
// the daemon holds them in so a client's list does not reshuffle between polls.
func mcpServersPayload(servers []domain.ConversationMCPServer) []ConversationMCPServerPayload {
	if len(servers) == 0 {
		return nil
	}
	out := make([]ConversationMCPServerPayload, 0, len(servers))
	for _, server := range servers {
		out = append(out, ConversationMCPServerPayload{
			Name:          server.Name,
			Status:        server.Status,
			Error:         server.Error,
			FailureReason: server.FailureReason,
		})
	}
	return out
}

// activityDetailPayload folds accumulated command output into the typed payload.
//
// The two output sources are not equivalent and the client is told which it has.
// The streamed accumulation exists while the command runs and survives a command
// that never completes; the provider's own aggregate only appears on completion.
// Neither is complete -- measured on codex-cli 0.146.0, a command printing
// tick-1..tick-8 lost tick-1 from the delta stream and from the aggregate alike --
// so outputMayBePartial stays set either way. `outputSource` exists so the UI can
// explain WHY it is partial instead of hedging identically about both.
func activityDetailPayload(activity domain.ConversationActivity) map[string]any {
	detail := decodeDetail(activity.Detail)
	if activity.CommandOutput != "" {
		if detail == nil {
			detail = map[string]any{}
		}
		// The stream wins over the separately capped aggregate, including its
		// own truncation state.
		detail["output"] = activity.CommandOutput
		detail["outputSource"] = "stream"
		detail["outputMayBePartial"] = true
		if activity.CommandOutputTruncated {
			detail["outputTruncated"] = true
		} else {
			delete(detail, "outputTruncated")
		}
	}
	if activity.StreamedText != "" {
		if detail == nil {
			detail = map[string]any{}
		}
		key, truncatedKey := streamedTextKeys(activity.Kind)
		detail[key] = activity.StreamedText
		if activity.StreamedTextTruncated {
			detail[truncatedKey] = true
		}
	}
	return detail
}

// streamedTextKeys names the accumulated provider prose for the kind of activity it
// belongs to.
//
// One column, several meanings, because each activity kind has exactly one such
// stream: a command's is the keystrokes the agent sent into its terminal, a
// reasoning item's is the model's own summary. Naming them apart on the wire is what
// keeps a client from having to guess which it is looking at -- and specifically
// keeps terminalInput out of `output`, which the PTY already echoes.
func streamedTextKeys(kind domain.ActivityKind) (key, truncatedKey string) {
	switch kind {
	case domain.ActivityKindReasoning:
		// The same key the settled summary lands under, so a client reads one field
		// whether the item is still streaming or already finished.
		return "text", "textTruncated"
	case domain.ActivityKindCommand:
		return "terminalInput", "terminalInputTruncated"
	case domain.ActivityKindMCPTool:
		return "progress", "progressTruncated"
	case domain.ActivityKindFileChange:
		return "patchOutput", "patchOutputTruncated"
	default:
		return "streamedText", "streamedTextTruncated"
	}
}

// decodeDetail turns the stored payload into a JSON object. A payload this build
// cannot parse is dropped rather than emitted as a string the client would have
// to guess at.
func decodeDetail(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var detail map[string]any
	if err := json.Unmarshal(raw, &detail); err != nil {
		return nil
	}
	return detail
}

func optionalTimestamp(t *time.Time) *string {
	if t == nil {
		return nil
	}
	formatted := t.UTC().Format(time.RFC3339)
	return &formatted
}
