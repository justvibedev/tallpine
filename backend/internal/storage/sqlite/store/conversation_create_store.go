package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

type conversationCreateOptions struct {
	id            string
	scope         domain.ConversationScope
	project       domain.ProjectID
	session       domain.SessionID
	contextReset  *domain.ConversationActivity
	preserveOwner bool
	adoptQueue    bool
	now           time.Time
}

// CreateConversation opens a worker's session-scoped conversation or rebinds an
// orchestrator's project-scoped narrative to its current session. Returning the
// existing row makes controller restart and clean orchestrator replacement
// idempotent without splitting the project history.
func (s *Store) CreateConversation(
	ctx context.Context,
	id string,
	scope domain.ConversationScope,
	project domain.ProjectID,
	session domain.SessionID,
	now time.Time,
) (domain.ConversationRecord, error) {
	return s.createConversation(ctx, conversationCreateOptions{
		id:      id,
		scope:   scope,
		project: project,
		session: session,
		now:     now,
	})
}

// OpenNativeConversation opens the Tallpine projection for an existing native
// conversation without adopting another session's project narrative. A first
// Terminal -> Chat handoff may create its root, but existing project ownership
// must match; only a prepared provider handoff can transfer that ownership.
func (s *Store) OpenNativeConversation(ctx context.Context, id string, scope domain.ConversationScope, project domain.ProjectID, session domain.SessionID, now time.Time) (domain.ConversationRecord, error) {
	return s.createConversation(ctx, conversationCreateOptions{
		id: id, scope: scope, project: project, session: session, now: now, preserveOwner: true,
	})
}

// CreateReviewConversation opens the durable narrative for one Chat reviewer.
// Reviewer identity, rather than the parent worker session, is the conversation
// owner; a worker can therefore retain its own Chat narrative at the same time.
func (s *Store) CreateReviewConversation(ctx context.Context, id, reviewID string, project domain.ProjectID, session domain.SessionID, now time.Time) (domain.ConversationRecord, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var conversation domain.ConversationRecord
	rootBranchID := id + ":root"
	if err := s.inTx(ctx, "insert reviewer conversation", func(q *gen.Queries) error {
		review, err := q.GetReviewByID(ctx, reviewID)
		if err != nil {
			return fmt.Errorf("select reviewer %s: %w", reviewID, err)
		}
		if review.SessionID != session || review.ProjectID != project {
			return fmt.Errorf("reviewer %s is not the current chat owner", reviewID)
		}
		mode := string(domain.ReviewerInterfaceChat)
		if n, err := q.SetReviewInterfaceMode(ctx, gen.SetReviewInterfaceModeParams{InterfaceMode: mode, Column2: mode, Column3: mode, Column4: mode, UpdatedAt: now, ID: reviewID}); err != nil {
			return err
		} else if n != 1 {
			return fmt.Errorf("reviewer %s is not the current chat owner", reviewID)
		}
		if existing, err := q.SelectConversationByReview(ctx, nullableString(reviewID)); err == nil {
			conversation = conversationToDomain(existing)
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("select conversation for review %s: %w", reviewID, err)
		}
		if err := q.InsertReviewConversation(ctx, gen.InsertReviewConversationParams{
			ID: id, ProjectID: optionalProjectID(project), ReviewID: nullableString(reviewID), CurrentReviewID: nullableString(reviewID),
			ActiveBranchID: rootBranchID, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		if err := q.InsertReviewConversationBranch(ctx, gen.InsertReviewConversationBranchParams{
			ID: rootBranchID, ConversationID: id, SessionID: nullableString(string(session)), ReviewID: nullableString(reviewID),
			ProviderConversationID: review.ProviderConversationID, CreatedAt: now,
		}); err != nil {
			return err
		}
		conversation = domain.ConversationRecord{ID: id, Scope: domain.ConversationScopeReview, ProjectID: project, ReviewID: reviewID, SessionID: session, ActiveBranchID: rootBranchID, CreatedAt: now, UpdatedAt: now}
		return nil
	}); err != nil {
		return domain.ConversationRecord{}, fmt.Errorf("insert reviewer conversation %s: %w", reviewID, err)
	}
	return conversation, nil
}

// CreateProjectConversationWithContextReset rebinds an existing project
// conversation and records the fresh-context boundary in the same transaction.
// The boundary is written before provider startup so paged readers never expose
// the previous orchestrator's rows under the replacement session.
func (s *Store) CreateProjectConversationWithContextReset(
	ctx context.Context,
	id string,
	project domain.ProjectID,
	session domain.SessionID,
	reset domain.ConversationActivity,
	now time.Time,
) (domain.ConversationRecord, error) {
	return s.createConversation(ctx, conversationCreateOptions{
		id:           id,
		scope:        domain.ConversationScopeProject,
		project:      project,
		session:      session,
		contextReset: &reset,
		now:          now,
	})
}

// CreateProjectConversationAdoptingQueue opens the project conversation for an
// orchestrator's first controller like CreateProjectConversationWithContextReset,
// and in the same transaction moves everything the session queued in its
// provisioning-time session row into it (#6523). Doing both in one write means a
// prompt sent meanwhile lands either in the session row, and moves with the rest,
// or in the project row after them.
func (s *Store) CreateProjectConversationAdoptingQueue(
	ctx context.Context,
	id string,
	project domain.ProjectID,
	session domain.SessionID,
	reset domain.ConversationActivity,
	now time.Time,
) (domain.ConversationRecord, error) {
	return s.createConversation(ctx, conversationCreateOptions{
		id:           id,
		scope:        domain.ConversationScopeProject,
		project:      project,
		session:      session,
		contextReset: &reset,
		adoptQueue:   true,
		now:          now,
	})
}

func (s *Store) createConversation(
	ctx context.Context,
	options conversationCreateOptions,
) (domain.ConversationRecord, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	scope := options.scope
	if scope == domain.ConversationScopeProject {
		if existing, err := s.qw.SelectProjectConversation(ctx, optionalProjectID(options.project)); err == nil {
			if options.preserveOwner {
				if existing.CurrentSessionID == nil || *existing.CurrentSessionID != options.session {
					return domain.ConversationRecord{}, fmt.Errorf("project conversation %s is no longer owned by session %s", existing.ID, options.session)
				}
				return conversationToDomain(existing), nil
			}
			transactionName := "rebind project conversation"
			if options.contextReset != nil {
				transactionName += " with reset"
			}
			rebind := func(q *gen.Queries) error { return rebindProjectConversation(ctx, q, &existing, options) }
			err = s.inTx(ctx, transactionName, func(q *gen.Queries) error {
				if err := rebind(q); err != nil {
					return err
				}
				latest, err := adoptForeignQueuedTurns(ctx, q, existing.ID, options.session, options.now)
				if err != nil {
					return err
				}
				if latest > 0 {
					existing.LatestSequence = latest
				}
				if !options.adoptQueue {
					return nil
				}
				latest, err = adoptProvisioningQueue(ctx, q, options.session, existing.ID, options.now)
				if err != nil {
					return err
				}
				existing.LatestSequence = latest
				return nil
			})
			if err != nil {
				return domain.ConversationRecord{}, err
			}
			existing.CurrentSessionID = &options.session
			existing.UpdatedAt = options.now
			return conversationToDomain(existing), nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return domain.ConversationRecord{}, fmt.Errorf("select project conversation for %s: %w", options.project, err)
		}
	} else {
		scope = domain.ConversationScopeSession
		if existing, err := s.qw.SelectConversationBySession(ctx, &options.session); err == nil {
			return conversationToDomain(existing), nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return domain.ConversationRecord{}, fmt.Errorf("select conversation for %s: %w", options.session, err)
		}
	}

	var ownerSession *domain.SessionID
	if scope == domain.ConversationScopeSession {
		ownerSession = &options.session
	}
	rootBranchID := options.id + ":root"
	latestSequence, err := s.insertConversationTx(ctx, options, scope, ownerSession, rootBranchID)
	if err != nil {
		return domain.ConversationRecord{}, fmt.Errorf("insert conversation %s: %w", options.id, err)
	}

	return domain.ConversationRecord{
		ID:             options.id,
		Scope:          scope,
		ProjectID:      options.project,
		SessionID:      options.session,
		ActiveBranchID: rootBranchID,
		LatestSequence: latestSequence,
		CreatedAt:      options.now,
		UpdatedAt:      options.now,
	}, nil
}

// adoptProvisioningQueue moves the rows of every other session-scoped
// conversation the session is bound to into targetID, then detaches those rows.
// Turns, messages, and activities keep their IDs, so a turn id a client already
// holds still resolves for edit and cancel. Messages and activities go after the
// target's latest sequence, which is after any context boundary the caller just
// recorded. It returns the target's new latest sequence.
func adoptProvisioningQueue(
	ctx context.Context,
	q *gen.Queries,
	session domain.SessionID,
	targetID string,
	now time.Time,
) (int64, error) {
	target, err := q.SelectConversationByID(ctx, targetID)
	if err != nil {
		return 0, fmt.Errorf("select adopting conversation %s: %w", targetID, err)
	}
	sources, err := q.SelectProvisioningConversationsForSession(ctx,
		gen.SelectProvisioningConversationsForSessionParams{CurrentSessionID: &session, ID: targetID})
	if err != nil {
		return 0, fmt.Errorf("select provisioning conversations for %s: %w", session, err)
	}
	latest := target.LatestSequence
	for _, source := range sources {
		if err := retargetExcerptReferences(ctx, q, source.ID, targetID); err != nil {
			return 0, err
		}
		if err := q.MoveConversationTurns(ctx, gen.MoveConversationTurnsParams{
			TargetID: targetID, BranchID: target.ActiveBranchID, SourceID: source.ID,
		}); err != nil {
			return 0, fmt.Errorf("move turns from %s: %w", source.ID, err)
		}
		if err := q.MoveConversationMessages(ctx, gen.MoveConversationMessagesParams{
			TargetID: targetID, BranchID: target.ActiveBranchID, SequenceOffset: latest, SourceID: source.ID,
		}); err != nil {
			return 0, fmt.Errorf("move messages from %s: %w", source.ID, err)
		}
		if err := q.MoveConversationActivities(ctx, gen.MoveConversationActivitiesParams{
			TargetID: targetID, BranchID: target.ActiveBranchID, SequenceOffset: latest, SourceID: source.ID,
		}); err != nil {
			return 0, fmt.Errorf("move activities from %s: %w", source.ID, err)
		}
		if err := q.MoveConversationQueuedEditDeliveries(ctx, gen.MoveConversationQueuedEditDeliveriesParams{
			TargetID: targetID, SourceID: source.ID,
		}); err != nil {
			return 0, fmt.Errorf("move queued edit receipts from %s: %w", source.ID, err)
		}
		if err := q.AdvanceConversationSequence(ctx, gen.AdvanceConversationSequenceParams{
			LatestSequence: source.LatestSequence, UpdatedAt: now, ID: targetID,
		}); err != nil {
			return 0, fmt.Errorf("advance sequence of %s: %w", targetID, err)
		}
		latest += source.LatestSequence
		// Only a turn whose prompt already reached the target stays behind. Settle
		// it so nothing queued is left where no lookup can find it.
		if err := q.CancelAllQueuedConversationTurns(ctx, gen.CancelAllQueuedConversationTurnsParams{
			CompletedAt: timeToNullTime(now), ConversationID: source.ID,
		}); err != nil {
			return 0, fmt.Errorf("settle duplicate turns in %s: %w", source.ID, err)
		}
		if err := q.DetachSessionConversation(ctx, gen.DetachSessionConversationParams{
			UpdatedAt: now, ID: source.ID, CurrentSessionID: &session,
		}); err != nil {
			return 0, fmt.Errorf("detach provisioning conversation %s: %w", source.ID, err)
		}
	}
	return latest, nil
}

// retargetExcerptReferences points the excerpt references queued in sourceID at
// targetID before the rows move, because drain checks each reference against the
// conversation it runs in.
func retargetExcerptReferences(ctx context.Context, q *gen.Queries, sourceID, targetID string) error {
	rows, err := q.SelectConversationDeliveryContent(ctx, sourceID)
	if err != nil {
		return fmt.Errorf("select delivery content in %s: %w", sourceID, err)
	}
	for _, row := range rows {
		var content []ports.ChatContent
		if err := json.Unmarshal([]byte(row.DeliveryContentJson), &content); err != nil {
			// Drain fails a turn with corrupt content; moving it unchanged keeps that.
			continue
		}
		changed := false
		for i := range content {
			if excerpt := content[i].Excerpt; excerpt != nil && excerpt.Reference.ConversationID == sourceID {
				excerpt.Reference.ConversationID = targetID
				changed = true
			}
		}
		if !changed {
			continue
		}
		encoded, err := json.Marshal(content)
		if err != nil {
			return fmt.Errorf("encode delivery content of %s: %w", row.ID, err)
		}
		if err := q.SetConversationMessageDeliveryContent(ctx, gen.SetConversationMessageDeliveryContentParams{
			DeliveryContentJson: string(encoded), ID: row.ID,
		}); err != nil {
			return fmt.Errorf("retarget excerpts of %s: %w", row.ID, err)
		}
	}
	return nil
}

// rebindProjectConversation binds an existing project conversation to the new
// owner session and settles or inserts the context-reset boundary.
func rebindProjectConversation(ctx context.Context, q *gen.Queries, existing *gen.Conversation, options conversationCreateOptions) error {
	if err := q.BindProjectConversationSession(ctx, gen.BindProjectConversationSessionParams{
		CurrentSessionID: &options.session,
		UpdatedAt:        options.now,
		ID:               existing.ID,
	}); err != nil {
		return fmt.Errorf("bind project conversation %s to %s: %w", existing.ID, options.session, err)
	}
	if existing.CurrentSessionID == nil || *existing.CurrentSessionID != options.session {
		// The previous owner's reserved provider id dies with that owner.
		// While the conversation is untouched, release it so the new owner
		// starts on the same root instead of a child provider boundary.
		if _, err := q.ReleaseUntouchedConversationProvider(ctx, gen.ReleaseUntouchedConversationProviderParams{
			SessionID:       &options.session,
			ProviderScopeID: uuid.NewString(),
		}); err != nil {
			return fmt.Errorf("release untouched provider of project conversation %s: %w", existing.ID, err)
		}
		// Tool-server startup state and the provider's thread lifecycle belong to
		// the process that reported them (see clearProviderReportedState). The
		// caller returns this row to seed the new controller, which would write
		// the old values straight back on its first report.
		if err := clearProviderReportedState(ctx, q, existing.ID); err != nil {
			return err
		}
		existing.McpServersJson, existing.ThreadStateJson = sql.NullString{}, sql.NullString{}
	}
	if options.contextReset == nil ||
		existing.LatestSequence <= 0 ||
		options.contextReset.ProviderItemID == "" {
		return nil
	}
	reset := options.contextReset
	detail := string(reset.Detail)
	_, lookupErr := q.SelectConversationActivityByProviderItem(ctx,
		gen.SelectConversationActivityByProviderItemParams{
			ConversationID: existing.ID,
			ProviderItemID: reset.ProviderItemID,
		})
	if lookupErr == nil {
		return q.SettleConversationActivity(ctx, gen.SettleConversationActivityParams{
			Status:         reset.Status,
			Summary:        reset.Summary,
			DetailJson:     detail,
			UpdatedAt:      options.now,
			ConversationID: existing.ID,
			ProviderItemID: reset.ProviderItemID,
		})
	}
	if !errors.Is(lookupErr, sql.ErrNoRows) {
		return fmt.Errorf("lookup reset boundary %s: %w", reset.ProviderItemID, lookupErr)
	}
	sequence, seqErr := q.NextConversationSequence(ctx, gen.NextConversationSequenceParams{
		UpdatedAt: options.now,
		ID:        existing.ID,
	})
	if seqErr != nil {
		return fmt.Errorf("allocate reset boundary sequence: %w", seqErr)
	}
	existing.LatestSequence = sequence
	return q.InsertConversationActivity(ctx, gen.InsertConversationActivityParams{
		ID:             reset.ID,
		ConversationID: existing.ID,
		TurnID:         sql.NullString{},
		Sequence:       sequence,
		Kind:           reset.Kind,
		Status:         reset.Status,
		Summary:        reset.Summary,
		DetailJson:     detail,
		RequestID:      reset.RequestID,
		ProviderItemID: reset.ProviderItemID,
		CreatedAt:      options.now,
		UpdatedAt:      options.now,
	})
}

// insertConversationTx inserts a conversation with its root branch in one
// transaction and returns the latest sequence after adopting a provisioning queue.
func (s *Store) insertConversationTx(ctx context.Context, options conversationCreateOptions, scope domain.ConversationScope, ownerSession *domain.SessionID, rootBranchID string) (int64, error) {
	var latestSequence int64
	err := s.inTx(ctx, "insert conversation", func(q *gen.Queries) error {
		owner, getErr := q.GetSession(ctx, options.session)
		if getErr != nil {
			return fmt.Errorf("select controller session %s: %w", options.session, getErr)
		}
		if insertErr := q.InsertConversation(ctx, gen.InsertConversationParams{
			ID:               options.id,
			Scope:            scope,
			ProjectID:        optionalProjectID(options.project),
			SessionID:        ownerSession,
			CurrentSessionID: &options.session,
			ActiveBranchID:   rootBranchID,
			CreatedAt:        options.now,
			UpdatedAt:        options.now,
		}); insertErr != nil {
			return insertErr
		}
		if err := q.InsertConversationBranch(ctx, gen.InsertConversationBranchParams{
			ID:                     rootBranchID,
			ConversationID:         options.id,
			SessionID:              nullableString(string(options.session)),
			ProviderConversationID: owner.ProviderConversationID,
			ForkAfterSequence:      0,
			ProviderScopeID:        rootBranchID,
			ProviderIdsScoped:      1,
			CreatedAt:              options.now,
		}); err != nil {
			return err
		}
		if !options.adoptQueue || scope != domain.ConversationScopeProject {
			return nil
		}
		var adoptErr error
		latestSequence, adoptErr = adoptProvisioningQueue(ctx, q, options.session, options.id, options.now)
		return adoptErr
	})
	return latestSequence, err
}

// clearProviderReportedState resets what the previous provider process reported
// about a conversation: tool-server startup status and the thread lifecycle. A
// new owner may run another harness (a Codex-only server that failed to start
// would otherwise be shown on a Claude chat forever, as Claude never reports
// it), and its controller re-announces what is true for it. NULL means "never
// asked". Call it wherever a conversation changes owner or harness.
func clearProviderReportedState(ctx context.Context, q *gen.Queries, conversationID string) error {
	if err := q.UpdateConversationMcpServers(ctx, gen.UpdateConversationMcpServersParams{ID: conversationID}); err != nil {
		return fmt.Errorf("clear stale tool server state of conversation %s: %w", conversationID, err)
	}
	if err := q.UpdateConversationThreadState(ctx, gen.UpdateConversationThreadStateParams{ID: conversationID}); err != nil {
		return fmt.Errorf("clear stale thread state of conversation %s: %w", conversationID, err)
	}
	return nil
}
