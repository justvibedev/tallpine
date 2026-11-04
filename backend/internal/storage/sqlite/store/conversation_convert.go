package store

import (
	"database/sql"
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

func conversationToDomain(row gen.Conversation) domain.ConversationRecord {
	rec := domain.ConversationRecord{
		ID:             row.ID,
		Scope:          row.Scope,
		ProjectID:      projectIDValue(row.ProjectID),
		ActiveBranchID: row.ActiveBranchID,
		LatestSequence: row.LatestSequence,
		Settings: domain.ConversationSettings{
			Model:           row.Model.String,
			ReasoningEffort: row.ReasoningEffort.String,
			ApprovalMode:    domain.PermissionMode(row.ApprovalMode.String),
			OpenCodeMode:    row.OpencodeMode,

			AutoCompactWindow: row.AutoCompactWindow,
		},
		ProviderTitle: row.ProviderTitle,
		AppliedTitle:  row.AppliedTitle,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
	if row.CurrentSessionID != nil {
		rec.SessionID = *row.CurrentSessionID
	}
	rec.Usage = usageFromRow(row)
	rec.RateLimits = rateLimitsFromRow(row)
	if row.CompactedAt.Valid {
		compacted := row.CompactedAt.Time
		rec.CompactedAt = &compacted
	}
	rec.ModelReroute = decodeJSONColumn[domain.ConversationModelReroute](row.ModelRerouteJson)
	rec.Account = decodeJSONColumn[domain.ConversationAccount](row.AccountJson)
	if rec.Account != nil && rec.Account.AuthenticationState == "" {
		rec.Account.AuthenticationState = "unknown"
		if rec.Account.ReauthRequiredAt != nil {
			rec.Account.AuthenticationState = "required"
		}
	}
	rec.ThreadState = decodeJSONColumn[domain.ConversationThreadState](row.ThreadStateJson)
	if servers := decodeJSONColumn[[]domain.ConversationMCPServer](row.McpServersJson); servers != nil {
		rec.MCPServers = *servers
	}
	return rec
}

func conversationBranchToDomain(row gen.SelectConversationBranchRow) domain.ConversationBranch {
	return domain.ConversationBranch{
		ID:                     row.ID,
		ConversationID:         row.ConversationID,
		SessionID:              domain.SessionID(row.SessionID.String),
		ProviderConversationID: row.ProviderConversationID,
		ParentBranchID:         row.ParentBranchID.String,
		ForkAfterTurnID:        row.ForkAfterTurnID.String,
		ReplacedTurnID:         row.ReplacedTurnID.String,
		ReplacementTurnID:      row.ReplacementTurnID.String,
		ForkAfterSequence:      row.ForkAfterSequence,
		Strategy:               domain.NormalizeConversationBranchStrategy(domain.ConversationBranchStrategy(row.Strategy)),
		ReplayCutoffSequence:   row.ReplayCutoffSequence,
		ReplayTruncated:        row.ReplayTruncated != 0,
		ProviderBindingID:      row.ProviderBindingID,
		ProviderScopeID:        row.EffectiveProviderScopeID,
		ProviderIDsScoped:      row.ProviderIdsScoped != 0,
		Active:                 row.Active,
		CreatedAt:              row.CreatedAt,
		HandoffNote:            row.HandoffNote,
	}
}

func conversationBranchListToDomain(row gen.SelectConversationBranchesRow) domain.ConversationBranch {
	return domain.ConversationBranch{
		ID:                     row.ID,
		ConversationID:         row.ConversationID,
		SessionID:              domain.SessionID(row.SessionID.String),
		ProviderConversationID: row.ProviderConversationID,
		ParentBranchID:         row.ParentBranchID.String,
		ForkAfterTurnID:        row.ForkAfterTurnID.String,
		ReplacedTurnID:         row.ReplacedTurnID.String,
		ReplacementTurnID:      row.ReplacementTurnID.String,
		ForkAfterSequence:      row.ForkAfterSequence,
		Strategy:               domain.NormalizeConversationBranchStrategy(domain.ConversationBranchStrategy(row.Strategy)),
		ReplayCutoffSequence:   row.ReplayCutoffSequence,
		ReplayTruncated:        row.ReplayTruncated != 0,
		ProviderBindingID:      row.ProviderBindingID,
		ProviderScopeID:        row.EffectiveProviderScopeID,
		ProviderIDsScoped:      row.ProviderIdsScoped != 0,
		Active:                 row.Active,
		CreatedAt:              row.CreatedAt,
		HandoffNote:            row.HandoffNote,
	}
}

// decodeJSONColumn reads one of the conversation's latest-wins JSON columns.
//
// A payload this build cannot parse yields nil rather than a zero value, which
// matters because nil is how a reader learns the provider never reported. A
// half-decoded account or thread state would be worse than none: only the first
// looks authoritative.
func decodeJSONColumn[T any](column sql.NullString) *T {
	if !column.Valid || column.String == "" {
		return nil
	}
	var value T
	if err := json.Unmarshal([]byte(column.String), &value); err != nil {
		return nil
	}
	return &value
}

// usageFromRow returns nil when the provider never reported, so a client can tell
// "no usage yet" from "a conversation using zero tokens" -- the second cannot
// happen, and showing an empty meter for the first would be a claim Tallpine has not
// earned.
func usageFromRow(row gen.Conversation) *domain.ConversationUsage {
	if !row.ContextUsed.Valid && !row.UsageTotalTokens.Valid && !row.UsageCost.Valid {
		return nil
	}
	usage := &domain.ConversationUsage{
		ContextUsed:   row.ContextUsed.Int64,
		ContextWindow: row.ContextWindow.Int64,
		InputTokens:   row.UsageInputTokens.Int64,
		OutputTokens:  row.UsageOutputTokens.Int64,
		CachedTokens:  row.UsageCachedTokens.Int64,
		TotalTokens:   row.UsageTotalTokens.Int64,
		Currency:      row.UsageCurrency.String,
	}
	if row.UsageCost.Valid {
		cost := row.UsageCost.Float64
		usage.Cost = &cost
	}
	return usage
}

func nullableCost(value *float64) sql.NullFloat64 {
	if value == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *value, Valid: true}
}

// rateLimitsFromRow reconstitutes the quota position, restoring the port's
// negative "not reported" convention for a window the provider omitted.
func rateLimitsFromRow(row gen.Conversation) *domain.ConversationRateLimits {
	if !row.RateLimitPrimaryPercent.Valid && !row.RateLimitSecondaryPercent.Valid {
		return nil
	}
	limits := &domain.ConversationRateLimits{
		PrimaryUsedPercent:       -1,
		SecondaryUsedPercent:     -1,
		PrimaryResetsInSeconds:   row.RateLimitPrimaryResetsIn.Int64,
		SecondaryResetsInSeconds: row.RateLimitSecondaryResetsIn.Int64,
		PlanLabel:                row.RateLimitPlan.String,
	}
	if row.RateLimitPrimaryPercent.Valid {
		limits.PrimaryUsedPercent = row.RateLimitPrimaryPercent.Float64
	}
	if row.RateLimitSecondaryPercent.Valid {
		limits.SecondaryUsedPercent = row.RateLimitSecondaryPercent.Float64
	}
	return limits
}

func turnToDomain(row gen.ConversationTurn) domain.ConversationTurn {
	turn := domain.ConversationTurn{
		ID:                 row.ID,
		ConversationID:     row.ConversationID,
		BranchID:           row.BranchID,
		HandledBySessionID: row.HandledBySessionID,
		ProviderTurnID:     row.ProviderTurnID,
		ProviderInputText:  row.ProviderInputText,
		State:              row.State,
		ErrorMessage:       row.ErrorMessage,
		RequestedAt:        row.RequestedAt,
	}
	if row.RetryOfTurnID.Valid {
		turn.RetryOfTurnID = row.RetryOfTurnID.String
	}
	if row.StartedAt.Valid {
		started := row.StartedAt.Time
		turn.StartedAt = &started
	}
	if row.CompletedAt.Valid {
		completed := row.CompletedAt.Time
		turn.CompletedAt = &completed
	}
	if turn.State == domain.TurnStateFailed {
		turn.UsageLimit = domain.ParseConversationUsageLimit(turn.ErrorMessage, turn.CompletedAt)
		if turn.UsageLimit != nil {
			if row.UsageLimitResetsAt.Valid {
				reset := row.UsageLimitResetsAt.Time
				turn.UsageLimit.ResetsAt = &reset
			}
			turn.UsageLimit.AutoResume = row.UsageLimitAutoResume != 0
			turn.UsageLimit.ResumeError = row.UsageLimitResumeError
		}
	}
	if row.DiffJson != "" {
		var diff domain.ConversationTurnDiff
		// A payload this build cannot parse is dropped rather than surfaced half
		// decoded: a diff summary that lists some of the files is worse than one
		// that admits it has nothing, because only the first looks authoritative.
		if err := json.Unmarshal([]byte(row.DiffJson), &diff); err == nil {
			turn.Diff = &diff
		}
	}
	if row.RolledBackAt.Valid {
		rolledBack := row.RolledBackAt.Time
		turn.RolledBackAt = &rolledBack
	}
	if row.PlanJson != "" {
		var plan domain.ConversationPlan
		// Dropped rather than surfaced half decoded, the same rule the diff follows: a
		// plan showing some of its steps is worse than one that admits it has nothing,
		// because only the first looks like the agent's actual plan.
		if err := json.Unmarshal([]byte(row.PlanJson), &plan); err == nil {
			turn.Plan = &plan
		}
	}
	return turn
}

func messageToDomain(row gen.ConversationMessage) domain.ConversationMessage {
	msg := domain.ConversationMessage{
		ID:                  row.ID,
		ConversationID:      row.ConversationID,
		Sequence:            row.Sequence,
		Revision:            row.Revision,
		Role:                row.Role,
		Origin:              row.Origin,
		Text:                row.Text,
		Streaming:           row.Streaming != 0,
		ProviderItemID:      row.ProviderItemID,
		ClientMessageID:     row.ClientMessageID,
		ClientPayloadHash:   row.ClientPayloadHash.String,
		DeliveryContentJSON: row.DeliveryContentJson,
		Continuation:        row.Continuation != 0,
		SenderSessionID:     row.SenderSessionID,
		SenderProjectID:     row.SenderProjectID,
		SenderDisplayName:   row.SenderDisplayName,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
	}
	if row.TurnID.Valid {
		msg.TurnID = row.TurnID.String
	}
	return msg
}

func activityToDomain(row gen.ConversationActivity) domain.ConversationActivity {
	activity := domain.ConversationActivity{
		ID:                     row.ID,
		ConversationID:         row.ConversationID,
		Sequence:               row.Sequence,
		Revision:               row.Revision,
		Kind:                   row.Kind,
		Status:                 row.Status,
		Summary:                row.Summary,
		RequestID:              row.RequestID,
		ProviderItemID:         row.ProviderItemID,
		CreatedAt:              row.CreatedAt,
		UpdatedAt:              row.UpdatedAt,
		CommandOutput:          row.CommandOutput,
		CommandOutputTruncated: row.CommandOutputTruncated != 0,
		StreamedText:           row.StreamedText,
		StreamedTextTruncated:  row.StreamedTextTruncated != 0,
	}
	if row.TurnID.Valid {
		activity.TurnID = row.TurnID.String
	}
	if row.DetailJson != "" && json.Valid([]byte(row.DetailJson)) {
		activity.Detail = []byte(row.DetailJson)
	}
	return activity
}
