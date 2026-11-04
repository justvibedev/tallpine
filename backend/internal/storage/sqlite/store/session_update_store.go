package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// UpdateSession writes the general mutable state of an existing session. The
// provisioning state is owned by SetSessionProvisionState so stale lifecycle
// snapshots cannot overwrite asynchronous start progress.
func (s *Store) UpdateSession(ctx context.Context, rec domain.SessionRecord) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "update session owner", func(q *gen.Queries) error {
		if err := q.UpdateSession(ctx, recordToUpdate(rec)); err != nil {
			return err
		}
		return q.ClaimCarriedTurnFallbacks(ctx, string(rec.ID))
	})
}

// SetSessionHibernated changes only the durable sleep marker if the caller's
// session snapshot is still current. Passing nil clears the marker on wake.
func (s *Store) SetSessionHibernated(ctx context.Context, id domain.SessionID, expectedRevision int64, at *time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionHibernated(ctx, gen.SetSessionHibernatedParams{
		HibernatedAt:     timePtrToNullTime(at),
		ID:               id,
		ExpectedRevision: expectedRevision,
	})
	if err != nil {
		return false, fmt.Errorf("set session hibernation for %s: %w", id, err)
	}
	return rows > 0, nil
}

// MarkHarnessSessionsStale marks the live launches of harness as running a
// release that version replaced, and returns how many it marked.
func (s *Store) MarkHarnessSessionsStale(ctx context.Context, harness domain.AgentHarness, version string, at time.Time) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.MarkHarnessSessionsStale(ctx, gen.MarkHarnessSessionsStaleParams{
		Since: sql.NullTime{Time: at, Valid: true}, Version: version, Harness: harness,
	})
	if err != nil {
		return 0, fmt.Errorf("mark %s sessions stale: %w", harness, err)
	}
	return rows, nil
}

// SetSessionVersionSwitchFailed records that moving the session's current
// launch to version failed.
func (s *Store) SetSessionVersionSwitchFailed(ctx context.Context, id domain.SessionID, version string, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.qw.SetSessionVersionSwitchFailed(ctx, gen.SetSessionVersionSwitchFailedParams{
		FailedAt: sql.NullTime{Time: at, Valid: true}, Version: version, ID: id,
	}); err != nil {
		return fmt.Errorf("record version switch failure for %s: %w", id, err)
	}
	return nil
}

// ListVersionRefreshChatCandidates lists idle Chat sessions, quiet since
// idleBefore, whose current launch runs a replaced harness release.
func (s *Store) ListVersionRefreshChatCandidates(ctx context.Context, idleBefore time.Time) ([]domain.SessionID, error) {
	ids, err := s.qr.ListVersionRefreshChatCandidates(ctx, idleBefore)
	if err != nil {
		return nil, fmt.Errorf("list chats for version refresh: %w", err)
	}
	return ids, nil
}

// ListVersionRefreshTerminalCandidates lists idle, natively resumable terminal
// sessions, quiet since idleBefore, whose current launch runs a replaced release.
func (s *Store) ListVersionRefreshTerminalCandidates(ctx context.Context, idleBefore time.Time) ([]domain.SessionID, error) {
	ids, err := s.qr.ListVersionRefreshTerminalCandidates(ctx, idleBefore)
	if err != nil {
		return nil, fmt.Errorf("list terminals for version refresh: %w", err)
	}
	return ids, nil
}

// CountStaleBinarySessions counts live launches still on a replaced release, per harness.
func (s *Store) CountStaleBinarySessions(ctx context.Context) (map[domain.AgentHarness]int64, error) {
	rows, err := s.qr.CountStaleBinarySessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("count stale-binary sessions: %w", err)
	}
	out := make(map[domain.AgentHarness]int64, len(rows))
	for _, row := range rows {
		out[row.Harness] = row.Sessions
	}
	return out, nil
}

// SetSessionRuntimeReleased records that Tallpine destroys launchID's terminal on
// purpose, keeping the session. It writes nothing unless launchID is still the
// session's current launch.
func (s *Store) SetSessionRuntimeReleased(ctx context.Context, id domain.SessionID, launchID string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionRuntimeReleased(ctx, gen.SetSessionRuntimeReleasedParams{ID: id, LaunchID: launchID})
	if err != nil {
		return false, fmt.Errorf("set session runtime release for %s: %w", id, err)
	}
	return rows > 0, nil
}

// UpdateSessionModel changes only the selected model, leaving concurrent
// lifecycle and controller ownership updates intact.
func (s *Store) UpdateSessionModel(ctx context.Context, id domain.SessionID, model string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.UpdateSessionModel(ctx, gen.UpdateSessionModelParams{
		ID:    id,
		Model: model,
	})
	if err != nil {
		return false, fmt.Errorf("update session model for %s: %w", id, err)
	}
	return rows > 0, nil
}

// UpdateSessionArtifactOutput changes only artifact_dir and
// session_output_type, leaving concurrent lifecycle/controller/activity
// writes intact. lifecycle.Manager.ReconcileSessionOutputType uses this
// instead of a read-modify-write UpdateSession so a stale in-memory
// SessionRecord read before a concurrent termination (or other update) can
// never replay is_terminated, activity, runtime identity, or preview state
// backwards over that newer write.
func (s *Store) UpdateSessionArtifactOutput(ctx context.Context, id domain.SessionID, artifactDir string, outputType domain.SessionOutputType) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.UpdateSessionArtifactOutput(ctx, gen.UpdateSessionArtifactOutputParams{
		ID:                id,
		ArtifactDir:       artifactDir,
		SessionOutputType: string(outputType),
	})
	if err != nil {
		return false, fmt.Errorf("update session artifact output for %s: %w", id, err)
	}
	return rows > 0, nil
}

// UpdateBrowserCapabilityVerifier rotates only the verifier when the caller's
// controller-owner snapshot is still current. It deliberately leaves every
// other mutable session field, including user-visible recency, untouched.
func (s *Store) UpdateBrowserCapabilityVerifier(
	ctx context.Context,
	id domain.SessionID,
	expected domain.SessionControllerOwner,
	verifier string,
) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.UpdateBrowserCapabilityVerifier(ctx, gen.UpdateBrowserCapabilityVerifierParams{
		BrowserCapabilityVerifier:      verifier,
		ID:                             id,
		ExpectedHarness:                expected.Harness,
		ExpectedSessionMode:            domain.NormalizeSessionMode(expected.Mode),
		ExpectedIsTerminated:           expected.IsTerminated,
		ExpectedRuntimeLaunchID:        expected.RuntimeLaunchID,
		ExpectedAgentSessionID:         expected.AgentSessionID,
		ExpectedAgentSessionIDLaunchID: expected.AgentSessionIDLaunchID,
		ExpectedProviderConversationID: expected.ProviderConversationID,
		ExpectedControllerGeneration:   expected.ControllerGeneration,
	})
	if err != nil {
		return false, fmt.Errorf("update browser capability verifier for %s: %w", id, err)
	}
	return rows > 0, nil
}

// UpdateSessionFromActivitySignal projects activity-derived session metadata
// only when the signal still belongs to the session's active harness launch.
func (s *Store) UpdateSessionFromActivitySignal(
	ctx context.Context,
	rec domain.SessionRecord,
	expectedRevision int64,
) (bool, error) {
	return s.updateSessionWithTurnFallback(ctx, rec, expectedRevision, nil, false)
}

// UpdateSessionWithTurnFallback commits activity, prior-result retirement, and
// any identified new turn atomically. Retirement does not require a new identity.
func (s *Store) UpdateSessionWithTurnFallback(ctx context.Context, rec domain.SessionRecord, expectedRevision int64, fallback domain.TurnFallback, retire bool) (bool, error) {
	if fallback.TurnID == "" {
		return s.updateSessionWithTurnFallback(ctx, rec, expectedRevision, nil, retire)
	}
	return s.updateSessionWithTurnFallback(ctx, rec, expectedRevision, &fallback, retire)
}

func (s *Store) updateSessionWithTurnFallback(ctx context.Context, rec domain.SessionRecord, expectedRevision int64, fallback *domain.TurnFallback, retire bool) (bool, error) {
	activity := normalActivity(rec.Activity, rec.UpdatedAt)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var rows int64
	project := func(q *gen.Queries) error {
		var err error
		rows, err = q.UpdateSessionFromActivitySignal(ctx, gen.UpdateSessionFromActivitySignalParams{
			ActivityState:                    activity.State,
			ActivityLastAt:                   activity.LastActivityAt,
			FirstSignalAt:                    timeToNullTime(rec.FirstSignalAt),
			AgentSessionID:                   rec.Metadata.AgentSessionID,
			AgentSessionIDLaunchID:           rec.Metadata.AgentSessionIDLaunchID,
			NativeIdentityObservedAt:         timeToNullTime(rec.Metadata.NativeIdentityObservedAt),
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
			ClaudeActivityFacts:              rec.Metadata.ClaudeActivityFacts,
			CodexActivityFacts:               rec.Metadata.CodexActivityFacts,
			UpdatedAt:                        rec.UpdatedAt,
			ID:                               rec.ID,
			ExpectedRevision:                 expectedRevision,
			ExpectedHarness:                  rec.Harness,
			ExpectedSessionMode:              domain.NormalizeSessionMode(rec.Mode),
			ExpectedRuntimeLaunchID:          rec.Metadata.RuntimeLaunchID,
			ExpectedControllerGeneration:     rec.Metadata.ControllerGeneration,
		})
		if err != nil || rows == 0 {
			return err
		}
		if retire {
			keep := ""
			if fallback != nil {
				keep = fallback.StableID()
			}
			if err := q.SupersedeTerminalFallbacks(ctx, gen.SupersedeTerminalFallbacksParams{SessionID: string(rec.ID), ID: keep}); err != nil {
				return err
			}
		}
		if fallback == nil {
			return nil
		}
		return recordTerminalFallback(ctx, q, *fallback)
	}
	var err error
	if fallback == nil && !retire {
		err = project(s.qw)
	} else {
		err = s.inTx(ctx, "project activity and turn fallback", project)
	}
	if err != nil {
		return false, fmt.Errorf("update session %s from activity signal: %w", rec.ID, err)
	}
	return rows > 0, nil
}

// RecordSessionLatestUserPrompt persists pane-delivered user direction without
// rewriting lifecycle ownership. Because the provider hook may be lost, the
// same atomic write clears any prior assistant pairing and trusted checkpoint
// provenance. An unresolved Stop boundary remains until a canonical main-turn
// hook supplies the missing boundary evidence.
func (s *Store) RecordSessionLatestUserPrompt(ctx context.Context, id domain.SessionID, prompt string, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var rows int64
	err := s.inTx(ctx, "record user work and retire fallback", func(q *gen.Queries) error {
		var err error
		rows, err = q.RecordSessionLatestUserPrompt(ctx, gen.RecordSessionLatestUserPromptParams{
			LatestUserPrompt: prompt,
			UpdatedAt:        timeToNullTime(updatedAt),
			ID:               id,
		})
		if err != nil || rows == 0 {
			return err
		}
		return q.SupersedeTerminalFallbacks(ctx, gen.SupersedeTerminalFallbacksParams{SessionID: string(id)})
	})
	if err != nil {
		return false, fmt.Errorf("record latest user prompt for session %s: %w", id, err)
	}
	return rows > 0, nil
}

// ClaimChatControllerGeneration makes generation the only Chat controller that
// may project provider events for this session. The narrow update avoids writing
// a stale full SessionRecord over lifecycle facts changed by another goroutine.
func (s *Store) ClaimChatControllerGeneration(
	ctx context.Context,
	id domain.SessionID,
	generation string,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "claim Chat fallback owner", func(q *gen.Queries) error {
		rows, err := q.ClaimChatControllerGeneration(ctx, gen.ClaimChatControllerGenerationParams{
			ControllerGeneration: generation,
			ID:                   id,
		})
		if err != nil {
			return fmt.Errorf("claim chat controller generation for %s: %w", id, err)
		}
		if rows == 0 {
			return fmt.Errorf("claim chat controller generation for %s: chat session not found", id)
		}
		return q.ClaimCarriedTurnFallbacks(ctx, string(id))
	})
}

// RenameSession updates only the user-facing display name for an existing
// session. It returns ok=false when the session id does not exist. The
// sessions_cdc_update trigger fans out a session_updated CDC event when the
// display name actually changes.
func (s *Store) RenameSession(ctx context.Context, id domain.SessionID, displayName string, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.RenameSession(ctx, gen.RenameSessionParams{
		ID:          id,
		DisplayName: displayName,
		UpdatedAt:   updatedAt,
	})
	if err != nil {
		return false, fmt.Errorf("rename session %s: %w", id, err)
	}
	return rows > 0, nil
}

// RenameSessionIfDisplayName applies a generated title only while the session
// still carries Tallpine's provisional name, so a concurrent human rename wins.
func (s *Store) RenameSessionIfDisplayName(
	ctx context.Context,
	id domain.SessionID,
	currentDisplayName, displayName string,
	updatedAt time.Time,
) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.RenameSessionIfDisplayName(ctx, gen.RenameSessionIfDisplayNameParams{
		ID:                 id,
		CurrentDisplayName: currentDisplayName,
		DisplayName:        displayName,
		UpdatedAt:          updatedAt,
	})
	if err != nil {
		return false, fmt.Errorf("rename session %s if unchanged: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionPinned updates the pinned status of a session.
func (s *Store) SetSessionPinned(ctx context.Context, id domain.SessionID, isPinned bool, pinnedAt *time.Time, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionPinned(ctx, gen.SetSessionPinnedParams{
		ID:        id,
		IsPinned:  isPinned,
		PinnedAt:  timePtrToNullTime(pinnedAt),
		UpdatedAt: updatedAt,
	})
	if err != nil {
		return false, fmt.Errorf("set session pinned %s: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionPreviewURL updates only the browser preview URL for an existing
// session. It returns ok=false when the session id does not exist. The
// sessions_cdc_update trigger fans out a session_updated CDC event when the
// preview URL actually changes.
func (s *Store) SetSessionPreviewURL(ctx context.Context, id domain.SessionID, previewURL string, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionPreviewURL(ctx, gen.SetSessionPreviewURLParams{
		ID:         id,
		PreviewURL: previewURL,
		UpdatedAt:  updatedAt,
	})
	if err != nil {
		return false, fmt.Errorf("set preview url for session %s: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionTerminateOnPRMerge updates the user's merge-completion lifecycle
// policy. It returns ok=false when the session id does not exist.
func (s *Store) SetSessionTerminateOnPRMerge(ctx context.Context, id domain.SessionID, terminate bool, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionTerminateOnPRMerge(ctx, gen.SetSessionTerminateOnPRMergeParams{
		ID:                 id,
		TerminateOnPRMerge: terminate,
		UpdatedAt:          updatedAt,
	})
	if err != nil {
		return false, fmt.Errorf("set terminate-on-pr-merge for session %s: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionAutoInjectReview persists a session's automatic review-injection policy.
func (s *Store) SetSessionAutoInjectReview(ctx context.Context, id domain.SessionID, autoInject bool, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionAutoInjectReview(ctx, gen.SetSessionAutoInjectReviewParams{
		ID:               id,
		AutoInjectReview: autoInject,
		UpdatedAt:        updatedAt,
	})
	if err != nil {
		return false, fmt.Errorf("set auto-inject review for session %s: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionAutoInjectCI persists the CI-failure injection policy for the
// session and every PR currently owned by it. Future PRs still inherit this
// value from the session row when first observed.
func (s *Store) SetSessionAutoInjectCI(ctx context.Context, id domain.SessionID, autoInject bool, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var updated bool
	err := s.inTx(ctx, fmt.Sprintf("set auto-inject CI for session %s", id), func(q *gen.Queries) error {
		rows, err := q.SetSessionAutoInjectCI(ctx, gen.SetSessionAutoInjectCIParams{
			ID:           id,
			AutoInjectCI: autoInject,
			UpdatedAt:    updatedAt,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return nil
		}
		updated = true
		if err := q.SetPRAutoInjectCIBySession(ctx, gen.SetPRAutoInjectCIBySessionParams{
			AutoInjectCI: autoInject,
			SessionID:    id,
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return updated, nil
}

// SetSessionReviewerConfig persists the reviewer preference for one session.
func (s *Store) SetSessionReviewerConfig(
	ctx context.Context,
	id domain.SessionID,
	harness domain.ReviewerHarness,
	config domain.AgentConfig,
	updatedAt time.Time,
) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	encoded, err := marshalAgentConfig(config)
	if err != nil {
		return false, fmt.Errorf("set reviewer config for %s: %w", id, err)
	}
	rows, err := s.qw.SetSessionReviewerConfig(ctx, gen.SetSessionReviewerConfigParams{
		ReviewerHarness:     harness,
		ReviewerAgentConfig: encoded,
		UpdatedAt:           updatedAt,
		ID:                  id,
	})
	if err != nil {
		return false, fmt.Errorf("set reviewer config for %s: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionAutoReview persists the daemon-side review automation toggle.
func (s *Store) SetSessionAutoReview(ctx context.Context, id domain.SessionID, enabled bool, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionAutoReview(ctx, gen.SetSessionAutoReviewParams{
		AutoReviewEnabled: enabled,
		UpdatedAt:         updatedAt,
		ID:                id,
	})
	if err != nil {
		return false, fmt.Errorf("set auto review for %s: %w", id, err)
	}
	return rows > 0, nil
}
