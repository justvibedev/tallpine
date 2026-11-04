package chat

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// settleOrphanedWork closes out anything a previous controller left behind.
//
// Best-effort by design: a failure here must not stop a session from coming back,
// because a session the user cannot reopen is worse than a stale row. Both
// failures are logged rather than swallowed.
func (s *Service) settleOrphanedWork(ctx context.Context, session domain.SessionID, conversationID string) {
	now := s.now()
	if err := s.store.SettleOrphanedRunningTurns(ctx, session, now); err != nil {
		s.log.Error("chat start: settle orphaned turns", "session", session, "error", err)
	}
	// An approval left pending can never be answered: the provider call it was
	// blocking died with the process that was holding it.
	if err := s.store.FailPendingApprovals(ctx, conversationID, now); err != nil {
		s.log.Error("chat start: close pending approvals", "session", session, "error", err)
	}
	if err := s.store.FailPendingInputs(ctx, conversationID, now); err != nil {
		s.log.Error("chat start: close pending input requests", "session", session, "error", err)
	}
}

// Start launches or resumes the Chat controller for a session.
//
// A resume that fails is reported as a failure rather than quietly becoming a new
// conversation: presenting unrelated history as continuous is worse than an error
// the user can act on.
func (s *Service) Start(ctx context.Context, cfg StartConfig) (*Controller, error) {
	owner := conversationOwner(cfg)
	gate := s.controllerGate(owner)
	if err := gate.lock(ctx); err != nil {
		return nil, err
	}
	endStart := s.beginStart(owner)
	controller, err := s.startLocked(ctx, cfg)
	if err == nil && cfg.ModelNotice != "" {
		s.postModelNotice(ctx, controller, cfg.ModelNotice)
	}
	queuedBehind := endStart()
	gate.unlock()
	if err == nil && queuedBehind {
		// A send queued while this start held the gate may have landed after the
		// controller's first drain; deliver it now rather than at the next turn.
		if drainErr := controller.drainOne(ctx); drainErr != nil {
			s.log.Warn("drain of sends queued during start failed", "session", cfg.SessionID, "error", drainErr)
		}
	}
	return controller, err
}

// PostSessionNotice puts a system row on a session's timeline now, for a notice
// that must not wait for a start that may fail. It reports false when the
// session has no conversation yet, so the notice can travel with the start.
func (s *Service) PostSessionNotice(ctx context.Context, id domain.SessionID, summary string) (bool, error) {
	conversation, err := s.store.ConversationForSession(ctx, id)
	if err != nil {
		return false, nil //nolint:nilerr // no conversation yet; the start posts it
	}
	return true, s.store.UpsertActivity(ctx, conversation.ID, "", domain.ConversationActivity{
		ID: s.newID(), Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
		Summary: summary, ProviderItemID: "ao-model-policy-" + s.newID(),
	}, s.now())
}

// postModelNotice puts a system row on the timeline saying a resume replaced a
// model the project now denies. Best effort: the resume has already succeeded.
func (s *Service) postModelNotice(ctx context.Context, controller *Controller, notice string) {
	err := controller.store.UpsertActivity(ctx, controller.conversation.ID, "", domain.ConversationActivity{
		ID: controller.newID(), Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
		Summary: notice, ProviderItemID: "ao-model-policy-" + controller.newID(),
	}, s.now())
	if err != nil {
		s.log.Warn("could not post the model policy notice", "session", controller.sessionID, "error", err)
	}
}
