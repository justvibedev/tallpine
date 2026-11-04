package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// CreateConversationBranch records a provider-thread child without changing the
// active controller. Activation is a separate transaction after the replacement
// provider controller is ready.
func (s *Store) CreateConversationBranch(
	ctx context.Context,
	branch domain.ConversationBranch,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	return s.inTx(ctx, "insert conversation branch", func(q *gen.Queries) error {
		return insertConversationBranchTx(ctx, q, branch, now)
	})
}

func insertConversationBranchTx(
	ctx context.Context,
	q *gen.Queries,
	branch domain.ConversationBranch,
	now time.Time,
) error {
	conversation, err := q.SelectConversationByID(ctx, branch.ConversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: conversation %s", domain.ErrNoConversationBranch, branch.ConversationID)
	}
	if err != nil {
		return fmt.Errorf("select conversation %s: %w", branch.ConversationID, err)
	}
	if branch.SessionID == "" && conversation.CurrentSessionID != nil {
		branch.SessionID = *conversation.CurrentSessionID
	}
	if branch.ParentBranchID != "" {
		if _, err := q.SelectConversationBranch(ctx, gen.SelectConversationBranchParams{
			ConversationID: branch.ConversationID,
			BranchID:       branch.ParentBranchID,
		}); err != nil {
			return fmt.Errorf("parent branch %s is not in conversation %s: %w",
				branch.ParentBranchID, branch.ConversationID, err)
		}
	}
	for label, turnID := range map[string]string{
		"fork-after":  branch.ForkAfterTurnID,
		"replaced":    branch.ReplacedTurnID,
		"replacement": branch.ReplacementTurnID,
	} {
		if turnID == "" {
			continue
		}
		turn, turnErr := q.SelectConversationTurnByID(ctx, turnID)
		if turnErr != nil || turn.ConversationID != branch.ConversationID {
			if turnErr == nil {
				turnErr = ErrConversationTurnNotFound
			}
			return fmt.Errorf("%s turn %s is not in conversation %s: %w",
				label, turnID, branch.ConversationID, turnErr)
		}
	}
	if now.IsZero() {
		now = branch.CreatedAt
	}
	if err := q.InsertConversationBranch(ctx, gen.InsertConversationBranchParams{
		ID:                     branch.ID,
		ConversationID:         branch.ConversationID,
		SessionID:              nullableString(string(branch.SessionID)),
		ProviderConversationID: branch.ProviderConversationID,
		ParentBranchID:         nullableString(branch.ParentBranchID),
		ForkAfterTurnID:        nullableString(branch.ForkAfterTurnID),
		ReplacedTurnID:         nullableString(branch.ReplacedTurnID),
		ReplacementTurnID:      nullableString(branch.ReplacementTurnID),
		ForkAfterSequence:      branch.ForkAfterSequence,
		Strategy:               string(domain.NormalizeConversationBranchStrategy(branch.Strategy)),
		ReplayCutoffSequence:   branch.ReplayCutoffSequence,
		ReplayTruncated:        boolInt(branch.ReplayTruncated),
		ProviderScopeID:        branch.ProviderScopeID,
		ProviderIdsScoped:      boolInt(branch.ProviderIDsScoped),
		CreatedAt:              now,
		HandoffNote:            branch.HandoffNote,
	}); err != nil {
		return fmt.Errorf("insert conversation branch %s: %w", branch.ID, err)
	}
	return nil
}

// CreateAndActivateConversationBranch publishes a ready provider branch and the
// session controller generation in one transaction. The caller retains the
// fenced source controller until this commit succeeds.
func (s *Store) CreateAndActivateConversationBranch(
	ctx context.Context,
	sessionID domain.SessionID,
	branch domain.ConversationBranch,
	generation string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	return s.inTx(ctx, "create and activate conversation branch", func(q *gen.Queries) error {
		if err := insertAndActivateConversationBranchTx(ctx, q, branch, now); err != nil {
			return err
		}
		sessionRows, err := q.ActivateConversationBranchSession(ctx, gen.ActivateConversationBranchSessionParams{
			ProviderConversationID: branch.ProviderConversationID,
			ControllerGeneration:   generation,
			UpdatedAt:              now,
			ID:                     sessionID,
		})
		if err != nil {
			return fmt.Errorf("move session controller: %w", err)
		}
		if sessionRows != 1 {
			return fmt.Errorf("move session controller: live chat session %s not found", sessionID)
		}
		return nil
	})
}

// CommitProviderSwitch atomically publishes the fresh provider branch and the
// target harness/controller metadata. Provider startup happens before this
// method; until it commits, the old branch remains the durable owner.
func (s *Store) CommitProviderSwitch(ctx context.Context, rec domain.SessionRecord, branch domain.ConversationBranch, generation string, settings domain.ConversationSettings, activity domain.ConversationActivity) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "commit provider switch", func(q *gen.Queries) error {
		if err := insertAndActivateConversationBranchTx(ctx, q, branch, rec.UpdatedAt); err != nil {
			return err
		}
		sessionRows, err := q.ActivateConversationBranchSession(ctx, gen.ActivateConversationBranchSessionParams{
			ProviderConversationID: branch.ProviderConversationID, ControllerGeneration: generation, UpdatedAt: rec.UpdatedAt, ID: rec.ID,
		})
		if err != nil {
			return fmt.Errorf("move session controller: %w", err)
		} else if sessionRows != 1 {
			return fmt.Errorf("move session controller: live chat session %s not found", rec.ID)
		}
		if err := q.UpdateSession(ctx, recordToUpdate(rec)); err != nil {
			return fmt.Errorf("update switched session: %w", err)
		}
		if err := q.UpdateConversationTurnSettings(ctx, gen.UpdateConversationTurnSettingsParams{
			Model: nullableString(settings.Model), ReasoningEffort: nullableString(settings.ReasoningEffort),
			ApprovalMode: nullableString(string(settings.ApprovalMode)), OpencodeMode: settings.OpenCodeMode,
			AutoCompactWindow: settings.AutoCompactWindow, UpdatedAt: rec.UpdatedAt, ID: branch.ConversationID,
		}); err != nil {
			return fmt.Errorf("save switched conversation settings: %w", err)
		}
		sequence, err := q.NextConversationSequence(ctx, gen.NextConversationSequenceParams{UpdatedAt: rec.UpdatedAt, ID: branch.ConversationID})
		if err != nil {
			return fmt.Errorf("allocate provider switch activity sequence: %w", err)
		}
		return q.InsertConversationActivity(ctx, gen.InsertConversationActivityParams{
			ID: activity.ID, ConversationID: branch.ConversationID, Sequence: sequence,
			Kind: activity.Kind, Status: activity.Status, Summary: activity.Summary,
			DetailJson: string(activity.Detail), RequestID: activity.RequestID,
			ProviderItemID: activity.ProviderItemID, CreatedAt: rec.UpdatedAt, UpdatedAt: rec.UpdatedAt,
		})
	})
}

// CreateAndActivateReviewConversationBranch publishes a fresh provider branch
// with the review's controller generation. Reviewer Chat does not own the
// worker session controller, so its boundary must claim the review row.
func (s *Store) CreateAndActivateReviewConversationBranch(
	ctx context.Context,
	reviewID string,
	branch domain.ConversationBranch,
	generation string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	return s.inTx(ctx, "create and activate reviewer conversation branch", func(q *gen.Queries) error {
		if err := insertAndActivateConversationBranchTx(ctx, q, branch, now); err != nil {
			return err
		}
		reviewRows, err := q.ClaimReviewChatController(ctx, gen.ClaimReviewChatControllerParams{
			ProviderConversationID: branch.ProviderConversationID,
			ControllerGeneration:   generation,
			UpdatedAt:              now,
			ID:                     reviewID,
		})
		if err != nil {
			return fmt.Errorf("move reviewer controller: %w", err)
		}
		if reviewRows != 1 {
			return fmt.Errorf("move reviewer controller: chat review %s not found", reviewID)
		}
		return nil
	})
}

func insertAndActivateConversationBranchTx(ctx context.Context, q *gen.Queries, branch domain.ConversationBranch, now time.Time) error {
	if err := insertConversationBranchTx(ctx, q, branch, now); err != nil {
		return err
	}
	rows, err := q.ActivateConversationBranch(ctx, gen.ActivateConversationBranchParams{
		ActiveBranchID: branch.ID,
		UpdatedAt:      now,
		ID:             branch.ConversationID,
	})
	if err != nil {
		return fmt.Errorf("move conversation head: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("move conversation head: conversation %s not found", branch.ConversationID)
	}
	return nil
}

// CommitChatSpawn publishes a reserved fresh-provider boundary and the complete
// lifecycle-owned session record in one transaction. It is intentionally
// separate from CreateAndActivateConversationBranch: ordinary ControllerReady
// must not commit MarkSpawned first and leave the previous provider branch
// active if the boundary write fails.
func (s *Store) CommitChatSpawn(
	ctx context.Context,
	rec domain.SessionRecord,
	branch domain.ConversationBranch,
) error {
	return s.commitChatSpawn(ctx, rec, branch, nil, nil)
}

// CommitChatSpawnPrepared stages the reserved boundary and controller generation,
// projects a reconciled native replay onto that branch, then publishes the live
// session as one transaction. The staged ownership is invisible outside the
// transaction, so input cannot reach the controller before its history exists.
func (s *Store) CommitChatSpawnPrepared(
	ctx context.Context,
	rec domain.SessionRecord,
	branch domain.ConversationBranch,
	handoff *domain.ChatProviderHandoff,
	prepare func(context.Context) error,
) error {
	if prepare == nil {
		return errors.New("commit Chat spawn: provider-history preparation is missing")
	}
	return s.commitChatSpawn(ctx, rec, branch, handoff, prepare)
}

func (s *Store) commitChatSpawn(
	ctx context.Context,
	rec domain.SessionRecord,
	branch domain.ConversationBranch,
	handoff *domain.ChatProviderHandoff,
	prepare func(context.Context) error,
) error {
	if rec.ID == "" || branch.ID == "" || branch.ConversationID == "" ||
		branch.SessionID != rec.ID || branch.ParentBranchID == "" ||
		branch.ProviderConversationID == "" ||
		branch.ProviderConversationID != rec.Metadata.ProviderConversationID ||
		branch.ProviderScopeID != branch.ID || rec.Metadata.ControllerGeneration == "" {
		return errors.New("commit Chat spawn: incomplete or mismatched provider boundary ownership")
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "commit Chat spawn", func(q *gen.Queries) error {
		owner, err := q.GetSession(ctx, rec.ID)
		if err != nil {
			return fmt.Errorf("select Chat session %s: %w", rec.ID, err)
		}
		if domain.NormalizeSessionMode(owner.SessionMode) != domain.SessionModeChat {
			return fmt.Errorf("session %s is not in Chat mode", rec.ID)
		}
		if handoff != nil && (handoff.BoundaryID != branch.ID || handoff.ConversationID != branch.ConversationID ||
			handoff.PreviousBranchID != branch.ParentBranchID ||
			rowToRecord(owner).ControllerOwner() != handoff.ExpectedControllerOwner) {
			return errors.New("native Chat handoff controller ownership changed")
		}
		conversation, err := q.SelectConversationByID(ctx, branch.ConversationID)
		if err != nil {
			return fmt.Errorf("select conversation %s: %w", branch.ConversationID, err)
		}
		expectedSession := rec.ID
		if handoff != nil {
			expectedSession = handoff.PreviousSessionID
			if conversation.LatestSequence != handoff.PreviousSequence {
				return errors.New("native Chat handoff history changed")
			}
		}
		if conversation.CurrentSessionID == nil || *conversation.CurrentSessionID != expectedSession {
			return fmt.Errorf("conversation %s is no longer owned by session %s",
				branch.ConversationID, rec.ID)
		}
		if conversation.ActiveBranchID != branch.ParentBranchID {
			return fmt.Errorf("conversation %s active branch changed from %s to %s",
				branch.ConversationID, branch.ParentBranchID, conversation.ActiveBranchID)
		}
		if expectedSession != rec.ID {
			previous, err := q.GetSession(ctx, expectedSession)
			if err != nil {
				return err
			}
			if conversation.Scope != domain.ConversationScopeProject || previous.ProjectID == nil || *previous.ProjectID != rec.ProjectID ||
				!previous.IsTerminated || !rec.CreatedAt.After(previous.CreatedAt) {
				return errors.New("native Chat handoff project owner is not a retired predecessor")
			}
			sessions, err := q.ListSessionsByProject(ctx, optionalProjectID(rec.ProjectID))
			if err != nil {
				return err
			}
			recLane, err := q.GetSessionLane(ctx, string(rec.ID))
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			for _, session := range sessions {
				// Only orchestrators of the same lane compete for the project
				// conversation; laned orchestrators own session-scoped chats.
				other, laneErr := q.GetSessionLane(ctx, string(session.ID))
				if laneErr != nil && !errors.Is(laneErr, sql.ErrNoRows) {
					return laneErr
				}
				if other.LaneItemID != recLane.LaneItemID {
					continue
				}
				if session.Kind == domain.KindOrchestrator && session.ID != rec.ID && !session.IsTerminated {
					return errors.New("native Chat handoff has a competing live orchestrator")
				}
			}
			if err := q.BindProjectConversationSession(ctx, gen.BindProjectConversationSessionParams{
				CurrentSessionID: &rec.ID, UpdatedAt: rec.UpdatedAt, ID: conversation.ID,
			}); err != nil {
				return err
			}
		}
		if err := insertConversationBranchTx(ctx, q, branch, branch.CreatedAt); err != nil {
			return err
		}
		rows, err := q.ActivateConversationBranch(ctx, gen.ActivateConversationBranchParams{
			ActiveBranchID: branch.ID,
			UpdatedAt:      rec.UpdatedAt,
			ID:             branch.ConversationID,
		})
		if err != nil {
			return fmt.Errorf("move conversation head: %w", err)
		}
		if rows != 1 {
			return fmt.Errorf("move conversation head: conversation %s not found", branch.ConversationID)
		}
		if err := moveUndispatchedIntakeTx(ctx, q, rec.ID, branch); err != nil {
			return err
		}
		if prepare != nil {
			// Provider-event projection is fenced by controller generation. Stage
			// that generation only inside this transaction, after the new branch is
			// active, so the replay lands in the new provider namespace while the
			// terminated target remains unavailable to every concurrent reader.
			rows, err := q.ClaimChatControllerGeneration(ctx, gen.ClaimChatControllerGenerationParams{
				ControllerGeneration: rec.Metadata.ControllerGeneration,
				ID:                   rec.ID,
			})
			if err != nil {
				return fmt.Errorf("stage Chat controller generation: %w", err)
			}
			if rows != 1 {
				return fmt.Errorf("stage Chat controller generation: Chat session %s not found", rec.ID)
			}
			txCtx := context.WithValue(ctx, conversationProjectionTxKey{}, q)
			if err := prepare(txCtx); err != nil {
				return fmt.Errorf("project native provider history: %w", err)
			}
		}
		if err := q.UpdateSession(ctx, recordToUpdate(rec)); err != nil {
			return fmt.Errorf("commit Chat session owner: %w", err)
		}
		return q.ClaimCarriedTurnFallbacks(ctx, string(rec.ID))
	})
}

// moveUndispatchedIntakeTx hands the session's queued, never-dispatched turns
// to the fresh provider boundary. They will run on that provider, and native
// replay only correlates turns inside the active provider scope; left on the
// parent branch, a restart would import the replayed prompt a second time.
func moveUndispatchedIntakeTx(ctx context.Context, q *gen.Queries, session domain.SessionID, branch domain.ConversationBranch) error {
	if err := q.MoveUndispatchedTurnMessagesToBranch(ctx, gen.MoveUndispatchedTurnMessagesToBranchParams{
		BranchID: branch.ID, ConversationID: branch.ConversationID, ParentBranchID: branch.ParentBranchID, SessionID: session,
	}); err != nil {
		return fmt.Errorf("move undispatched intake messages: %w", err)
	}
	if err := q.MoveUndispatchedTurnActivitiesToBranch(ctx, gen.MoveUndispatchedTurnActivitiesToBranchParams{
		BranchID: branch.ID, ConversationID: branch.ConversationID, ParentBranchID: branch.ParentBranchID, SessionID: session,
	}); err != nil {
		return fmt.Errorf("move undispatched intake activities: %w", err)
	}
	if err := q.MoveUndispatchedTurnsToBranch(ctx, gen.MoveUndispatchedTurnsToBranchParams{
		BranchID: branch.ID, ConversationID: branch.ConversationID, ParentBranchID: branch.ParentBranchID, SessionID: session,
	}); err != nil {
		return fmt.Errorf("move undispatched intake turns: %w", err)
	}
	return nil
}
