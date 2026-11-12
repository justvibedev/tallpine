package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Compact asks the provider to summarize earlier history and reclaim context.
//
// Why this exists at all: every turn re-sends the whole conversation, so context
// fills on its own and a long conversation eventually cannot accept another turn.
// Compaction is the difference between a session that works for an hour and one
// that works for a day.
//
// The result reports what is about to be reclaimed, not what was. The provider
// accepts the request immediately and does the work as its own turn over the next
// ten seconds or so; the settled figures arrive on the timeline as a compaction
// entry.
func (s *Service) Compact(ctx context.Context, id domain.SessionID) (ports.ChatCompactionResult, error) {
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return ports.ChatCompactionResult{}, err
	}
	defer release()
	return controller.Compact(ctx)
}

// ReloadMCPServers restarts the provider's tool servers for this session.
//
// The failure it addresses is not the agent's. An MCP server that fails to start
// stays failed for the life of the provider process, so a session loses a tool for
// good and the only other way back is to throw the conversation away. Same for a
// server whose config changed on disk, or one whose auth expired: the agent has no
// way to notice and nothing it can do about it.
//
// It returns the servers' state so a caller sees the outcome without polling,
// though the provider's own startup notifications remain the authoritative report
// and land on the conversation regardless of who asked.
func (s *Service) ReloadMCPServers(
	ctx context.Context,
	id domain.SessionID,
) ([]domain.ConversationMCPServer, error) {
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
	return controller.ReloadMCPServers(ctx)
}

// RetryTurn re-dispatches a failed turn's durable prompt as a new turn.
// The content is loaded from Tallpine's own rows, never from the caller, so the daemon
// owns what gets sent again. The current next-turn settings apply.
func (s *Service) RetryTurn(
	ctx context.Context,
	id domain.SessionID,
	turnID string,
) (domain.ConversationTurn, error) {
	return s.retryTurn(ctx, id, turnID, false, false)
}

func (s *Service) retryTurn(ctx context.Context, id domain.SessionID, turnID string, autoResume, failover bool) (domain.ConversationTurn, error) {
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	defer release()
	var result domain.ConversationTurn
	if autoResume {
		result, err = controller.retryUsageLimitTurn(ctx, turnID)
	} else if failover {
		result, err = controller.retryFailoverTurn(ctx, turnID)
	} else {
		result, err = controller.RetryTurn(ctx, turnID)
	}
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	return result, nil
}

// SetTurnSettings records the provider choices for this session's next turn.
//
// Applied per turn, so nothing restarts: the running turn keeps whatever it was
// dispatched with, and the choice takes effect on the next one.
func (s *Service) SetTurnSettings(
	ctx context.Context,
	id domain.SessionID,
	settings domain.ConversationSettings,
) (domain.ConversationSettings, error) {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return domain.ConversationSettings{}, err
	}
	if err := s.checkModelPolicy(ctx, id, settings.Model); err != nil {
		return domain.ConversationSettings{}, err
	}
	controller, release, err := s.workingController(ctx, id)
	if errors.Is(err, ErrNoController) {
		// A stopped or exited agent has no controller to apply the choice to, but the
		// conversation keeps it: the next start restores the stored model.
		return s.setStoppedTurnSettings(ctx, id, settings, err)
	}
	if err != nil {
		return domain.ConversationSettings{}, err
	}
	defer release()
	return s.setTurnSettings(ctx, controller, domain.SessionConversationOwner(id), settings)
}

// setStoppedTurnSettings saves a pick made while no controller runs. noController
// is returned when the session has no conversation yet, so there is nothing to keep it on.
func (s *Service) setStoppedTurnSettings(ctx context.Context, id domain.SessionID, settings domain.ConversationSettings, noController error) (domain.ConversationSettings, error) {
	gate := s.controllerGate(domain.SessionConversationOwner(id))
	if err := gate.lock(ctx); err != nil {
		return domain.ConversationSettings{}, err
	}
	defer gate.unlock()
	// Re-checked under the gate: a start may have raced in, and then it owns the settings.
	if controller, err := s.Controller(id); err == nil && controller.State() != ports.ChatControllerStopped {
		return domain.ConversationSettings{}, ErrControllerHandoff
	}
	conversation, err := s.store.ConversationForSession(ctx, id)
	if errors.Is(err, domain.ErrNoConversation) {
		return domain.ConversationSettings{}, noController
	}
	if err != nil {
		return domain.ConversationSettings{}, fmt.Errorf("conversation for %s: %w", id, err)
	}
	previous := conversation.Settings
	settings.OpenCodeMode = previous.OpenCodeMode
	if err := s.store.SetConversationSettings(ctx, conversation.ID, settings, s.now()); err != nil {
		return domain.ConversationSettings{}, fmt.Errorf("record conversation settings: %w", err)
	}
	s.persistPickedModel(id, previous, settings)
	return settings, nil
}

// ErrReviewerPermissionsFixed rejects changes to unattended reviewer approval policy.
var ErrReviewerPermissionsFixed = errors.New("reviewer approval permissions are fixed")

// SetTurnSettingsForOwner records choices without changing another owner's settings.
func (s *Service) SetTurnSettingsForOwner(ctx context.Context, owner domain.ConversationOwner, settings domain.ConversationSettings) (domain.ConversationSettings, error) {
	controller, err := s.ControllerForOwner(owner)
	if err != nil {
		return domain.ConversationSettings{}, err
	}
	// A reviewer chat is policed against the project its review belongs to.
	if owner.Kind == domain.ConversationOwnerReview {
		if err := s.checkProjectModelPolicy(ctx, controller.conversation.ProjectID, settings.Model); err != nil {
			return domain.ConversationSettings{}, err
		}
	}
	return s.setTurnSettings(ctx, controller, owner, settings)
}

func (s *Service) setTurnSettings(ctx context.Context, controller *Controller, owner domain.ConversationOwner, settings domain.ConversationSettings) (domain.ConversationSettings, error) {
	controller.configMu.Lock()
	defer controller.configMu.Unlock()
	// The turn-settings endpoint does not own provider session mode choices.
	previous := controller.Settings()
	if owner.Kind == domain.ConversationOwnerReview && settings.ApprovalMode != previous.ApprovalMode {
		return domain.ConversationSettings{}, ErrReviewerPermissionsFixed
	}
	settings.OpenCodeMode = previous.OpenCodeMode
	if err := controller.SetSettings(ctx, settings); err != nil {
		return domain.ConversationSettings{}, err
	}
	if owner.Kind == domain.ConversationOwnerSession {
		s.persistPickedModel(domain.SessionID(owner.ID), previous, settings)
	}
	applied := controller.Settings()
	var pending bool
	if owner.Kind == domain.ConversationOwnerSession && settings.AutoCompactWindow != previous.AutoCompactWindow {
		pending = s.reconcileCompaction(ctx, controller)
	} else {
		pending = s.compactionPending(ctx, controller)
	}
	s.overlayCompactionView(ctx, &applied, controller, pending)
	return applied, nil
}

// persistPickedModel keeps session metadata in sync with the Chat model choice,
// including clearing an override before a later TUI rebuild.
func (s *Service) persistPickedModel(id domain.SessionID, previous, next domain.ConversationSettings) {
	model := strings.TrimSpace(next.Model)
	if model == strings.TrimSpace(previous.Model) || s.onModelChanged == nil {
		return
	}
	s.onModelChanged(id, model)
}
