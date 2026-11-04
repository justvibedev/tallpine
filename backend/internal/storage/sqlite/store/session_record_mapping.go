package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

func mapListSessionsByProjectRows(rows []gen.ListSessionsByProjectRow) []domain.SessionRecord {
	out := make([]domain.SessionRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, listSessionsByProjectRowToRecord(r))
	}
	return out
}

func mapListAllSessionsRows(rows []gen.ListAllSessionsRow) []domain.SessionRecord {
	out := make([]domain.SessionRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, listAllSessionsRowToRecord(r))
	}
	return out
}

func rowToRecord(row gen.GetSessionRow) domain.SessionRecord {
	return domain.SessionRecord{
		Revision:                  row.Revision,
		ID:                        row.ID,
		ClientRequestID:           row.ClientRequestID,
		ClientRequestHash:         row.ClientRequestHash,
		ClientRequestCommitted:    row.ClientRequestCommitted,
		ProjectID:                 projectIDValue(row.ProjectID),
		AutomationRunID:           row.AutomationRunID,
		AutomationLaunchCompleted: row.AutomationLaunchCompleted,
		IssueID:                   row.IssueID,
		Kind:                      row.Kind,
		Harness:                   row.Harness,
		ReviewerHarness:           row.ReviewerHarness,
		ReviewerConfig:            unmarshalAgentConfig(row.ReviewerAgentConfig),
		AutoReviewEnabled:         row.AutoReviewEnabled,
		DisplayName:               row.DisplayName,
		Mode:                      domain.NormalizeSessionMode(row.SessionMode),
		Activity: domain.Activity{
			State:          row.ActivityState,
			LastActivityAt: row.ActivityLastAt,
		},
		FirstSignalAt:      nullTimeToTime(row.FirstSignalAt),
		IsTerminated:       row.IsTerminated,
		HibernatedAt:       nullTimeToTimePtr(row.HibernatedAt),
		StaleBinary:        staleBinaryFromRow(row),
		IsPinned:           row.IsPinned,
		PinnedAt:           nullTimeToTimePtr(row.PinnedAt),
		TerminateOnPRMerge: row.TerminateOnPRMerge,
		AutoInjectReview:   row.AutoInjectReview,
		AutoInjectCI:       row.AutoInjectCI,
		OutputType:         normalizeSessionOutputType(domain.SessionOutputType(row.SessionOutputType)),
		Metadata: domain.SessionMetadata{
			Branch:                           row.Branch,
			WorkspacePath:                    row.WorkspacePath,
			WorkspaceRepoPath:                row.WorkspaceRepoPath,
			DiffBaseSHA:                      row.DiffBaseSha,
			DiffBaseRef:                      row.DiffBaseRef,
			RuntimeHandleID:                  row.RuntimeHandleID,
			RuntimeLaunchID:                  row.RuntimeLaunchID,
			RuntimeReleasedLaunchID:          row.RuntimeReleasedLaunchID,
			AgentSessionID:                   row.AgentSessionID,
			AgentSessionIDLaunchID:           row.AgentSessionIDLaunchID,
			NativeIdentityObservedAt:         nullTimeToTime(row.NativeIdentityObservedAt),
			Prompt:                           row.Prompt,
			LatestUserPrompt:                 row.LatestUserPrompt,
			LatestUserPromptAt:               nullTimeToTime(row.LatestUserPromptAt),
			LatestInteractionAt:              nullTimeToTime(row.LatestInteractionAt),
			LatestAssistantUpdate:            row.LatestAssistantUpdate,
			LatestAssistantUpdateAt:          nullTimeToTime(row.LatestAssistantUpdateAt),
			ConversationCheckpointState:      row.ConversationCheckpointState,
			ConversationCheckpointGeneration: row.ConversationCheckpointGeneration,
			ConversationCheckpointNativeID:   row.ConversationCheckpointNativeID,
			ConversationCheckpointTurnID:     row.ConversationCheckpointTurnID,
			NativeCheckpointEvidence:         row.NativeCheckpointEvidence,
			ConversationCheckpointUnsettled:  row.ConversationCheckpointUnsettled,
			NativeTranscriptPath:             row.NativeTranscriptPath,
			ClaudeActivityFacts:              row.ClaudeActivityFacts,
			CodexActivityFacts:               row.CodexActivityFacts,
			PreviewURL:                       row.PreviewURL,
			PreviewRevision:                  row.PreviewRevision,
			ArtifactDir:                      row.ArtifactDir,
			BrowserCapabilityVerifier:        row.BrowserCapabilityVerifier,
			ProviderConversationID:           row.ProviderConversationID,
			ControllerGeneration:             row.ControllerGeneration,
			Model:                            row.Model,
			Effort:                           row.Effort,
			Permissions:                      domain.PermissionMode(row.SessionPermissions),
		},
		RequestedOutput:   domain.RequestedOutput(row.RequestedOutput),
		Role:              domain.SessionRole(row.Role),
		CleanupGeneration: row.CleanupGeneration,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
		ProvisionState:    row.ProvisionState.WithDefault(),
		ProvisionError:    row.ProvisionError,
		ProvisionSteps:    decodeProvisionSteps(row.ProvisionSteps),
		BranchState:       decodeBranchState(row.BranchState),
		BranchNaming:      row.BranchNaming,
		TemporaryBranch:   row.TemporaryBranch,
		IsTaskPreparation: row.IsTaskPreparation,
	}
}

// staleBinaryFromRow reports a stale-binary mark only for the launch it was
// taken on: the Chat controller generation or the terminal launch id.
func staleBinaryFromRow(row gen.GetSessionRow) *domain.SessionStaleBinary {
	if !row.StaleBinarySince.Valid {
		return nil
	}
	current := row.RuntimeLaunchID
	if domain.NormalizeSessionMode(row.SessionMode) == domain.SessionModeChat {
		current = row.ControllerGeneration
	}
	if row.StaleBinaryLaunch != current {
		return nil
	}
	return &domain.SessionStaleBinary{
		Version:        row.StaleBinaryVersion,
		Since:          row.StaleBinarySince.Time,
		SwitchFailedAt: nullTimeToTimePtr(row.StaleBinarySwitchFailedAt),
	}
}

// decodeProvisionSteps reads the start-up checklist. A malformed value reads as
// no checklist. Missing setup evidence never authorizes interrupted-request
// cleanup, and ProvisionState stays the start's authoritative outcome.
func decodeProvisionSteps(raw string) []domain.SessionProvisionStep {
	if raw == "" {
		return nil
	}
	var steps []domain.SessionProvisionStep
	if err := json.Unmarshal([]byte(raw), &steps); err != nil {
		return nil
	}
	return steps
}

// decodeBranchState reads the observed branch facts. A missing or malformed
// value reads as not yet observed, never as a branch with no commits.
func decodeBranchState(raw string) *domain.SessionBranchState {
	if raw == "" {
		return nil
	}
	var state domain.SessionBranchState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return nil
	}
	return &state
}

func getSessionRowToRecord(row gen.GetSessionRow) domain.SessionRecord {
	return rowToRecord(row)
}

func listSessionsByProjectRowToRecord(row gen.ListSessionsByProjectRow) domain.SessionRecord {
	return rowToRecord(gen.GetSessionRow(row))
}

func listAllSessionsRowToRecord(row gen.ListAllSessionsRow) domain.SessionRecord {
	return rowToRecord(gen.GetSessionRow(row))
}

func recordToInsert(rec domain.SessionRecord, num int64) gen.InsertSessionParams {
	activity := normalActivity(rec.Activity, rec.CreatedAt)
	return gen.InsertSessionParams{
		ID:                               rec.ID,
		ProjectID:                        optionalProjectID(rec.ProjectID),
		Num:                              num,
		IssueID:                          rec.IssueID,
		Kind:                             rec.Kind,
		Harness:                          rec.Harness,
		ReviewerHarness:                  rec.ReviewerHarness,
		ReviewerAgentConfig:              mustMarshalAgentConfig(rec.ReviewerConfig),
		AutoReviewEnabled:                rec.AutoReviewEnabled,
		DisplayName:                      rec.DisplayName,
		ActivityState:                    activity.State,
		ActivityLastAt:                   activity.LastActivityAt,
		FirstSignalAt:                    timeToNullTime(rec.FirstSignalAt),
		IsTerminated:                     rec.IsTerminated,
		IsPinned:                         rec.IsPinned,
		PinnedAt:                         timePtrToNullTime(rec.PinnedAt),
		Branch:                           rec.Metadata.Branch,
		WorkspacePath:                    rec.Metadata.WorkspacePath,
		WorkspaceRepoPath:                rec.Metadata.WorkspaceRepoPath,
		DiffBaseSha:                      rec.Metadata.DiffBaseSHA,
		DiffBaseRef:                      rec.Metadata.DiffBaseRef,
		RuntimeHandleID:                  rec.Metadata.RuntimeHandleID,
		RuntimeLaunchID:                  rec.Metadata.RuntimeLaunchID,
		AgentSessionID:                   rec.Metadata.AgentSessionID,
		AgentSessionIDLaunchID:           rec.Metadata.AgentSessionIDLaunchID,
		NativeIdentityObservedAt:         timeToNullTime(rec.Metadata.NativeIdentityObservedAt),
		Prompt:                           rec.Metadata.Prompt,
		LatestUserPrompt:                 rec.Metadata.LatestUserPrompt,
		LatestUserPromptAt:               timeToNullTime(rec.Metadata.LatestUserPromptAt),
		LatestAssistantUpdate:            rec.Metadata.LatestAssistantUpdate,
		LatestAssistantUpdateAt:          timeToNullTime(rec.Metadata.LatestAssistantUpdateAt),
		ConversationCheckpointState:      normalizedConversationCheckpointState(rec.Metadata),
		ConversationCheckpointGeneration: rec.Metadata.ConversationCheckpointGeneration,
		ConversationCheckpointNativeID:   rec.Metadata.ConversationCheckpointNativeID,
		ConversationCheckpointTurnID:     rec.Metadata.ConversationCheckpointTurnID,
		NativeCheckpointEvidence:         rec.Metadata.NativeCheckpointEvidence,
		ConversationCheckpointUnsettled:  rec.Metadata.ConversationCheckpointUnsettled,
		NativeTranscriptPath:             rec.Metadata.NativeTranscriptPath,
		PreviewURL:                       rec.Metadata.PreviewURL,
		PreviewRevision:                  rec.Metadata.PreviewRevision,
		TerminateOnPRMerge:               rec.TerminateOnPRMerge,
		AutoInjectReview:                 rec.AutoInjectReview,
		AutoInjectCI:                     rec.AutoInjectCI,
		CleanupGeneration:                rec.CleanupGeneration,
		BrowserCapabilityVerifier:        rec.Metadata.BrowserCapabilityVerifier,
		ArtifactDir:                      rec.Metadata.ArtifactDir,
		SessionOutputType:                string(normalizeSessionOutputType(rec.OutputType)),
		SessionMode:                      domain.NormalizeSessionMode(rec.Mode),
		ProviderConversationID:           rec.Metadata.ProviderConversationID,
		ControllerGeneration:             rec.Metadata.ControllerGeneration,
		Model:                            rec.Metadata.Model,
		Effort:                           rec.Metadata.Effort,
		SessionPermissions:               string(rec.Metadata.Permissions),
		RequestedOutput:                  string(rec.RequestedOutput),
		Role:                             string(rec.Role),
		CreatedAt:                        rec.CreatedAt,
		UpdatedAt:                        rec.UpdatedAt,
		ProvisionState:                   rec.ProvisionState.WithDefault(),
		ProvisionError:                   rec.ProvisionError,
		IsTaskPreparation:                rec.IsTaskPreparation,
		AutomationRunID:                  rec.AutomationRunID,
		AutomationLaunchCompleted:        rec.AutomationLaunchCompleted,
		ClientRequestID:                  rec.ClientRequestID,
		ClientRequestHash:                rec.ClientRequestHash,
	}
}

// Permissions are immutable after insertion (or the focused legacy pin), so
// generic session updates cannot overwrite them with a stale record.
func recordToUpdate(rec domain.SessionRecord) gen.UpdateSessionParams {
	activity := normalActivity(rec.Activity, rec.UpdatedAt)
	return gen.UpdateSessionParams{
		ID:                               rec.ID,
		IssueID:                          rec.IssueID,
		Kind:                             rec.Kind,
		Harness:                          rec.Harness,
		ReviewerHarness:                  rec.ReviewerHarness,
		ReviewerAgentConfig:              mustMarshalAgentConfig(rec.ReviewerConfig),
		AutoReviewEnabled:                rec.AutoReviewEnabled,
		ActivityState:                    activity.State,
		ActivityLastAt:                   activity.LastActivityAt,
		FirstSignalAt:                    timeToNullTime(rec.FirstSignalAt),
		IsTerminated:                     rec.IsTerminated,
		IsPinned:                         rec.IsPinned,
		PinnedAt:                         timePtrToNullTime(rec.PinnedAt),
		TemporaryBranch:                  rec.Metadata.Branch,
		Branch:                           rec.Metadata.Branch,
		WorkspacePath:                    rec.Metadata.WorkspacePath,
		WorkspaceRepoPath:                rec.Metadata.WorkspaceRepoPath,
		DiffBaseSha:                      rec.Metadata.DiffBaseSHA,
		DiffBaseRef:                      rec.Metadata.DiffBaseRef,
		RuntimeHandleID:                  rec.Metadata.RuntimeHandleID,
		RuntimeLaunchID:                  rec.Metadata.RuntimeLaunchID,
		AgentSessionID:                   rec.Metadata.AgentSessionID,
		AgentSessionIDLaunchID:           rec.Metadata.AgentSessionIDLaunchID,
		NativeIdentityObservedAt:         timeToNullTime(rec.Metadata.NativeIdentityObservedAt),
		Prompt:                           rec.Metadata.Prompt,
		LatestUserPrompt:                 rec.Metadata.LatestUserPrompt,
		LatestUserPromptAt:               timeToNullTime(rec.Metadata.LatestUserPromptAt),
		LatestAssistantUpdate:            rec.Metadata.LatestAssistantUpdate,
		LatestAssistantUpdateAt:          timeToNullTime(rec.Metadata.LatestAssistantUpdateAt),
		ConversationCheckpointState:      normalizedConversationCheckpointState(rec.Metadata),
		ConversationCheckpointGeneration: rec.Metadata.ConversationCheckpointGeneration,
		ConversationCheckpointNativeID:   rec.Metadata.ConversationCheckpointNativeID,
		ConversationCheckpointTurnID:     rec.Metadata.ConversationCheckpointTurnID,
		NativeCheckpointEvidence:         rec.Metadata.NativeCheckpointEvidence,
		ConversationCheckpointUnsettled:  rec.Metadata.ConversationCheckpointUnsettled,
		NativeTranscriptPath:             rec.Metadata.NativeTranscriptPath,
		PreviewURL:                       rec.Metadata.PreviewURL,
		PreviewRevision:                  rec.Metadata.PreviewRevision,
		TerminateOnPRMerge:               rec.TerminateOnPRMerge,
		AutoInjectReview:                 rec.AutoInjectReview,
		AutoInjectCI:                     rec.AutoInjectCI,
		CleanupGeneration:                rec.CleanupGeneration,
		BrowserCapabilityVerifier:        rec.Metadata.BrowserCapabilityVerifier,
		ProviderConversationID:           rec.Metadata.ProviderConversationID,
		ControllerGeneration:             rec.Metadata.ControllerGeneration,
		Model:                            rec.Metadata.Model,
		Effort:                           rec.Metadata.Effort,
		UpdatedAt:                        rec.UpdatedAt,
		AutomationLaunchCompleted:        rec.AutomationLaunchCompleted,
	}
}

func marshalAgentConfig(cfg domain.AgentConfig) (string, error) {
	if cfg.IsZero() {
		return "", nil
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal agent config: %w", err)
	}
	return string(data), nil
}

func mustMarshalAgentConfig(cfg domain.AgentConfig) string {
	data, err := marshalAgentConfig(cfg)
	if err != nil {
		panic(err)
	}
	return data
}

func unmarshalAgentConfig(data string) domain.AgentConfig {
	if data == "" {
		return domain.AgentConfig{}
	}
	var cfg domain.AgentConfig
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		return domain.AgentConfig{}
	}
	return cfg
}

func normalizedConversationCheckpointState(metadata domain.SessionMetadata) domain.ConversationCheckpointState {
	if metadata.ConversationCheckpointState != "" {
		return metadata.ConversationCheckpointState
	}
	if metadata.LatestUserPrompt != "" || metadata.LatestAssistantUpdate != "" {
		return domain.ConversationCheckpointLegacy
	}
	return domain.ConversationCheckpointEmpty
}

func normalizeSessionOutputType(v domain.SessionOutputType) domain.SessionOutputType {
	if v == "" {
		return domain.SessionOutputNone
	}
	return v
}

// nullTimeToTime / timeToNullTime bridge the nullable first_signal_at column
// to the domain's zero-time convention (zero = no signal received yet).
func nullTimeToTime(t sql.NullTime) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

func timeToNullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

func nullTimeToTimePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

func timePtrToNullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

func normalActivity(a domain.Activity, fallback time.Time) domain.Activity {
	if a.State == "" {
		a.State = domain.ActivityIdle
	}
	if a.LastActivityAt.IsZero() {
		a.LastActivityAt = fallback
	}
	if a.LastActivityAt.IsZero() {
		a.LastActivityAt = time.Now().UTC()
	}
	// The driver stores a time.Time by its String() form, so the zone and any
	// monotonic reading survive into the column. Rows written from a local-zone
	// clock ("… +0700 +07 m=+995.1") then stop comparing as timestamps against
	// UTC rows, and activity_last_at is compared directly in SQL — the
	// activity predicates are one such comparison, and a mismatched row silently
	// matches zero rows there. Normalize every writer's value here
	// rather than trusting each caller's clock.
	a.LastActivityAt = a.LastActivityAt.UTC()
	return a
}
