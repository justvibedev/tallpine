package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// AppendUserMessage records an inbound message and the turn it opens.
//
// Idempotent on clientMessageID: a retried send returns the message and turn that
// already exist instead of opening a second provider turn. The caller must not
// dispatch to the provider when `created` is false.
func (s *Store) AppendUserMessage(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	generation string,
	msg domain.ConversationMessage,
	turnID string,
	now time.Time,
) (created bool, err error) {
	return s.appendUserMessage(ctx, conversationID, session, "", generation, msg, turnID, "", false, false, now)
}

// AppendStartingUserMessage queues a turn for a controller that is starting
// now (its start holds the controller gate), so a send does not wait out a
// slow resume (B496). Only the starting state is relaxed: a terminated session
// and a moved conversation are still refused.
func (s *Store) AppendStartingUserMessage(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	msg domain.ConversationMessage,
	turnID string,
	now time.Time,
) (created bool, err error) {
	return s.appendUserMessage(ctx, conversationID, session, "", "", msg, turnID, "", false, true, now)
}

// AppendReviewUserMessage preserves the worker session needed for lifecycle
// projection while recording the typed reviewer that owns the turn.
func (s *Store) AppendReviewUserMessage(ctx context.Context, conversationID string, session domain.SessionID, reviewID, generation string, msg domain.ConversationMessage, turnID string, now time.Time) (bool, error) {
	return s.appendUserMessage(ctx, conversationID, session, reviewID, generation, msg, turnID, "", false, false, now)
}

// AppendRetryUserMessage records a retry and its source as one transaction.
// Idempotency is owned by the explicit retry relation, not by caller-controlled
// client message ids.
func (s *Store) AppendRetryUserMessage(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	generation string,
	msg domain.ConversationMessage,
	turnID string,
	retryOfTurnID string,
	now time.Time,
) (created bool, err error) {
	return s.appendUserMessage(ctx, conversationID, session, "", generation, msg, turnID, retryOfTurnID, false, false, now)
}

// AppendAutoResumeRetryUserMessage consumes due consent in the retry transaction.
// Cancellation and account binding changes use the same writer boundary.
func (s *Store) AppendAutoResumeRetryUserMessage(ctx context.Context, conversationID string, session domain.SessionID, generation string, msg domain.ConversationMessage, turnID, retryOfTurnID string, now time.Time) (bool, error) {
	return s.appendUserMessage(ctx, conversationID, session, "", generation, msg, turnID, retryOfTurnID, true, false, now)
}

// AppendReviewRetryUserMessage atomically persists a retry for a reviewer chat.
func (s *Store) AppendReviewRetryUserMessage(ctx context.Context, conversationID string, session domain.SessionID, reviewID, generation string, msg domain.ConversationMessage, turnID, retryOfTurnID string, now time.Time) (bool, error) {
	return s.appendUserMessage(ctx, conversationID, session, reviewID, generation, msg, turnID, retryOfTurnID, false, false, now)
}

func (s *Store) appendUserMessage(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	reviewID string,
	generation string,
	msg domain.ConversationMessage,
	turnID string,
	retryOfTurnID string,
	autoResume bool,
	startingController bool,
	now time.Time,
) (created bool, err error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if generation == "" && reviewID == "" {
		if err := s.checkQueuedSession(ctx, conversationID, session, startingController); err != nil {
			return false, err
		}
	}

	if duplicate, err := s.duplicateUserMessage(ctx, conversationID, retryOfTurnID, msg); err != nil || duplicate {
		return false, err
	}

	err = s.inTx(ctx, "append user message", func(q *gen.Queries) error {
		if autoResume {
			if err := consumeDueAutoResume(ctx, q, conversationID, session, retryOfTurnID, now); err != nil {
				return err
			}
		}
		sequence, err := q.NextConversationSequence(ctx, gen.NextConversationSequenceParams{
			UpdatedAt: now,
			ID:        conversationID,
		})
		if err != nil {
			return fmt.Errorf("allocate sequence: %w", err)
		}

		if reviewID == "" {
			if err := q.SupersedeCompletedFallbacks(ctx, string(session)); err != nil {
				return err
			}
			if err := q.InsertConversationTurn(ctx, gen.InsertConversationTurnParams{ID: turnID, ConversationID: conversationID, HandledBySessionID: session, ControllerGeneration: generation, RetryOfTurnID: nullableString(retryOfTurnID), State: domain.TurnStateQueued, RequestedAt: now}); err != nil {
				return fmt.Errorf("insert turn: %w", err)
			}
		} else if err := q.InsertReviewConversationTurn(ctx, gen.InsertReviewConversationTurnParams{ID: turnID, ConversationID: conversationID, HandledBySessionID: session, HandledByReviewID: nullableString(reviewID), ControllerGeneration: generation, RetryOfTurnID: nullableString(retryOfTurnID), State: domain.TurnStateQueued, RequestedAt: now}); err != nil {
			return fmt.Errorf("insert reviewer turn: %w", err)
		}
		if retryOfTurnID != "" && !autoResume {
			if _, err := q.SetConversationTurnAutoResume(ctx, gen.SetConversationTurnAutoResumeParams{
				ID: retryOfTurnID, ConversationID: conversationID,
			}); err != nil {
				return fmt.Errorf("consume source auto-resume: %w", err)
			}
		}

		if err := q.InsertConversationMessage(ctx, gen.InsertConversationMessageParams{
			ID:                  msg.ID,
			ConversationID:      conversationID,
			TurnID:              sql.NullString{String: turnID, Valid: true},
			Sequence:            sequence,
			Role:                domain.MessageRoleUser,
			Origin:              msg.Origin,
			Text:                msg.Text,
			ProviderItemID:      "",
			ClientMessageID:     msg.ClientMessageID,
			ClientPayloadHash:   sql.NullString{String: msg.ClientPayloadHash, Valid: msg.ClientPayloadHash != ""},
			DeliveryContentJson: msg.DeliveryContentJSON,
			Continuation:        boolInt(msg.Continuation),
			SenderSessionID:     msg.SenderSessionID,
			SenderProjectID:     msg.SenderProjectID,
			SenderDisplayName:   msg.SenderDisplayName,
			CreatedAt:           now,
			UpdatedAt:           now,
		}); err != nil {
			return err
		}
		interactionAt := msg.InteractionAt
		if interactionAt.IsZero() {
			interactionAt = now
		}
		if msg.SenderSessionID != "" {
			if err := recordSessionInteraction(ctx, q, session, msg.SenderSessionID, interactionAt); err != nil {
				return err
			}
		}
		if msg.Origin == domain.MessageOriginHuman || msg.AuthoredByUser {
			if _, err := q.RecordSessionHumanMessage(ctx, gen.RecordSessionHumanMessageParams{
				ID:                 session,
				LatestUserPrompt:   msg.Text,
				LatestUserPromptAt: timeToNullTime(interactionAt),
			}); err != nil {
				return fmt.Errorf("record latest human message: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// ConversationMessageByClientID finds the durable normal-message outcome for an
// idempotent client delivery handle.
func (s *Store) ConversationMessageByClientID(
	ctx context.Context,
	conversationID, clientMessageID string,
) (domain.ConversationMessage, bool, error) {
	row, err := s.qr.SelectConversationMessageByClientID(ctx,
		gen.SelectConversationMessageByClientIDParams{
			ConversationID: conversationID, ClientMessageID: clientMessageID,
		})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationMessage{}, false, nil
	}
	if err != nil {
		return domain.ConversationMessage{}, false, err
	}
	return messageToDomain(row), true, nil
}

// ConversationMessages returns the durable transcript in sequence order.
func (s *Store) ConversationMessages(ctx context.Context, conversationID string) ([]domain.ConversationMessage, error) {
	rows, err := s.qr.SelectConversationMessages(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	messages := make([]domain.ConversationMessage, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, messageToDomain(row))
	}
	return messages, nil
}

// AdoptProviderTurn records a turn the provider started that Tallpine never dispatched.
//
// A compaction runs as its own provider turn, and so does work the provider
// resumes inside its own history. Without a row, every item those turns emit
// correlates to no turn: the activities lose their turn id and the timeline stops
// grouping them, which reads to a user as the conversation falling apart. Idempotent
// because the provider re-announces a turn on resume.
func (s *Store) AdoptProviderTurn(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	generation, turnID, providerTurnID string,
	now time.Time,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.AdoptProviderConversationTurn(ctx, gen.AdoptProviderConversationTurnParams{
		ID:                   turnID,
		ConversationID:       conversationID,
		HandledBySessionID:   session,
		ProviderTurnID:       providerTurnID,
		ControllerGeneration: generation,
		RequestedAt:          now,
		StartedAt:            sql.NullTime{Time: now, Valid: true},
	}); err != nil {
		return fmt.Errorf("adopt provider turn %s: %w", providerTurnID, err)
	}
	return nil
}

// AdoptReviewProviderTurn projects provider-initiated reviewer work with its
// typed owner. The worker session remains present for the existing lifecycle
// and CDC projection, but never substitutes for reviewer ownership.
func (s *Store) AdoptReviewProviderTurn(ctx context.Context, conversationID string, session domain.SessionID, reviewID, generation, turnID, providerTurnID string, now time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.AdoptReviewProviderConversationTurn(ctx, gen.AdoptReviewProviderConversationTurnParams{
		ID: turnID, ConversationID: conversationID, HandledBySessionID: session, HandledByReviewID: nullableString(reviewID),
		ProviderTurnID: providerTurnID, ControllerGeneration: generation, RequestedAt: now, StartedAt: sql.NullTime{Time: now, Valid: true},
	}); err != nil {
		return fmt.Errorf("adopt reviewer provider turn %s: %w", providerTurnID, err)
	}
	return nil
}

// AppendImportedUserMessage records a user prompt recovered from the provider's
// native thread. The enclosing turn must already have been adopted from the
// history's turn.started event.
//
// Idempotency is by provider turn rather than only by clientMessageID. A prompt Tallpine
// originally sent in Chat mode already belongs to that turn but may carry a
// different client id from the history adapter; checking the turn is what keeps a
// Chat -> TUI -> Chat round trip from duplicating it.
func (s *Store) AppendImportedUserMessage(
	ctx context.Context,
	conversationID, providerTurnID string,
	msg domain.ConversationMessage,
	now time.Time,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	_, err := q.SelectConversationUserMessageByTurn(ctx,
		gen.SelectConversationUserMessageByTurnParams{
			ConversationID: conversationID,
			ProviderTurnID: providerTurnID,
		})
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("lookup imported user message for turn %s: %w", providerTurnID, err)
	}

	turn, err := q.SelectConversationTurnByProviderID(ctx,
		gen.SelectConversationTurnByProviderIDParams{
			ConversationID: conversationID,
			ProviderTurnID: providerTurnID,
		})
	if err != nil {
		return fmt.Errorf("lookup imported provider turn %s: %w", providerTurnID, err)
	}
	sequence, err := q.NextConversationSequence(ctx, gen.NextConversationSequenceParams{
		UpdatedAt: now,
		ID:        conversationID,
	})
	if err != nil {
		return fmt.Errorf("allocate imported user message sequence: %w", err)
	}
	if err := q.InsertConversationMessage(ctx, gen.InsertConversationMessageParams{
		ID:                  msg.ID,
		ConversationID:      conversationID,
		TurnID:              sql.NullString{String: turn.ID, Valid: true},
		Sequence:            sequence,
		Role:                domain.MessageRoleUser,
		Origin:              domain.MessageOriginHuman,
		Text:                msg.Text,
		ProviderItemID:      msg.ProviderItemID,
		ClientMessageID:     msg.ClientMessageID,
		DeliveryContentJson: msg.DeliveryContentJSON,
		CreatedAt:           now,
		UpdatedAt:           now,
	}); err != nil {
		return fmt.Errorf("insert imported user message for turn %s: %w", providerTurnID, err)
	}
	return nil
}

type steerRetryInput struct {
	Text                       string              `json:"text"`
	Content                    []ports.ChatContent `json:"content"`
	ProviderInputText          string              `json:"providerInputText"`
	ProviderInputContent       []ports.ChatContent `json:"providerInputContent"`
	ProviderInputCaptured      bool                `json:"providerInputCaptured"`
	ProviderInputStoredOnTurn  bool                `json:"providerInputStoredOnTurn"`
	ProviderInputSourceTurnIDs []string            `json:"providerInputSourceTurnIds"`
}

type retryProviderInputItem struct {
	Text         string `json:"text"`
	ContentStart int    `json:"contentStart,omitempty"`
	ContentCount int    `json:"contentCount,omitempty"`
}

// RetryPrompt returns the durable human prompt of a failed turn, for
// re-dispatching it as a new turn. The content is loaded from Tallpine's own rows
// rather than from a client request that could be stale or substituted.
func (s *Store) RetryPrompt(ctx context.Context, conversationID, turnID string) (domain.RetryPrompt, error) {
	row, err := s.qr.SelectRetryableConversationPrompt(ctx, gen.SelectRetryableConversationPromptParams{
		ID: turnID, ConversationID: conversationID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RetryPrompt{}, fmt.Errorf("%w: %s", domain.ErrNoConversationTurn, turnID)
	}
	if err != nil {
		return domain.RetryPrompt{}, fmt.Errorf("read retry prompt %s: %w", turnID, err)
	}
	prompt := domain.RetryPrompt{
		Continuation:        row.Continuation != 0,
		Text:                row.Text,
		ProviderInputText:   row.ProviderInputText,
		Origin:              row.Origin,
		DeliveryContentJSON: row.DeliveryContentJson,
		ActiveLineage:       row.ActiveLineage,
	}
	activityDetails, err := s.qr.SelectConversationSteerRetryInputs(ctx, gen.SelectConversationSteerRetryInputsParams{
		ConversationID: conversationID, TurnID: sql.NullString{String: turnID, Valid: true},
	})
	if err != nil {
		return domain.RetryPrompt{}, fmt.Errorf("read steer inputs %s: %w", turnID, err)
	}
	if prompt.ProviderInputText == "" && len(activityDetails) == 0 {
		return prompt, nil
	}
	steers := make([]steerRetryInput, 0, len(activityDetails))
	steeredSourceIDs := make(map[string]bool)
	initialText := prompt.Text
	initialWasRendered := prompt.ProviderInputText != ""
	if initialWasRendered {
		initialText = prompt.ProviderInputText
	}
	for _, detail := range activityDetails {
		var steer steerRetryInput
		if err := json.Unmarshal([]byte(detail), &steer); err != nil {
			return domain.RetryPrompt{}, fmt.Errorf("decode steer input %s: %w", turnID, err)
		}
		if steer.ProviderInputStoredOnTurn && steer.ProviderInputText == prompt.ProviderInputText {
			// Older turns could save the first batch steer in this field instead of
			// the opening input. Its activity still identifies that steer.
			initialText = prompt.Text
			initialWasRendered = false
		}
		if steer.ProviderInputCaptured {
			for _, id := range steer.ProviderInputSourceTurnIDs {
				steeredSourceIDs[id] = true
			}
		}
		steers = append(steers, steer)
	}
	blocks, err := s.qr.SelectConversationProviderInputContent(ctx, gen.SelectConversationProviderInputContentParams{
		ConversationID: conversationID, TurnID: turnID,
	})
	if err != nil {
		return domain.RetryPrompt{}, fmt.Errorf("read provider input content %s: %w", turnID, err)
	}
	var content []ports.ChatContent
	for _, block := range blocks {
		if steeredSourceIDs[block.SourceTurnID] || block.DeliveryContentJson == "" {
			continue
		}
		var items []ports.ChatContent
		if err := json.Unmarshal([]byte(block.DeliveryContentJson), &items); err != nil {
			return domain.RetryPrompt{}, fmt.Errorf("decode provider input content %s: %w", turnID, err)
		}
		for _, item := range items {
			if initialWasRendered && ((item.Type == "excerpt" && item.Excerpt != nil) || (item.Type == "text" && item.Internal)) {
				continue
			}
			content = append(content, item)
		}
	}
	if len(steers) > 0 {
		inputs := []retryProviderInputItem{{Text: initialText, ContentCount: len(content)}}
		for _, steer := range steers {
			text := steer.Text
			blocks := steer.Content
			if steer.ProviderInputText != "" {
				text = steer.ProviderInputText
			}
			if steer.ProviderInputCaptured {
				blocks = steer.ProviderInputContent
			}
			inputs = append(inputs, retryProviderInputItem{
				Text: text, ContentStart: len(content), ContentCount: len(blocks),
			})
			content = append(content, blocks...)
		}
		encoded, err := json.Marshal(inputs)
		if err != nil {
			return domain.RetryPrompt{}, fmt.Errorf("encode provider inputs %s: %w", turnID, err)
		}
		prompt.ProviderInputText = "Tallpine prior provider inputs in order. Each JSON item is one complete input; content blocks follow contentStart and contentCount indices.\n" + string(encoded)
	}
	prompt.DeliveryContentJSON = ""
	if len(content) > 0 {
		encoded, err := json.Marshal(content)
		if err != nil {
			return domain.RetryPrompt{}, fmt.Errorf("encode provider input content %s: %w", turnID, err)
		}
		prompt.DeliveryContentJSON = string(encoded)
	}
	return prompt, nil
}

// RetryTurnIDForSource returns the retry attempt already linked to a source.
func (s *Store) RetryTurnIDForSource(ctx context.Context, conversationID, sourceTurnID string) (string, bool, error) {
	row, err := s.qr.SelectConversationRetryTurnIDBySource(ctx, gen.SelectConversationRetryTurnIDBySourceParams{
		ConversationID: conversationID,
		RetryOfTurnID:  sql.NullString{String: sourceTurnID, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return row, true, nil
}
