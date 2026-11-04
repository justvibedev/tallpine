package chat

import (
	"context"
	"errors"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Resolve answers a pending approval.
func (s *Service) Resolve(
	ctx context.Context,
	id domain.SessionID,
	requestID string,
	decision ports.ChatDecision,
) error {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return err
	}
	return controller.Resolve(ctx, requestID, decision)
}

// ResolveForOwner resolves a pending approval for an owner-specific controller.
func (s *Service) ResolveForOwner(ctx context.Context, owner domain.ConversationOwner, requestID string, decision ports.ChatDecision) error {
	controller, err := s.ControllerForOwner(owner)
	if err != nil {
		return err
	}
	return controller.Resolve(ctx, requestID, decision)
}

// ResolveInput answers a structured user-input request. It remains a separate
// command from approval resolution because the response carries typed form data
// (or URL consent), not a provider-offered permission id.
func (s *Service) ResolveInput(
	ctx context.Context,
	id domain.SessionID,
	requestID string,
	response ports.ChatInputResponse,
) error {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return err
	}
	return controller.ResolveInput(ctx, requestID, response)
}

// ResolveInputForOwner answers structured input for an owner-specific controller.
func (s *Service) ResolveInputForOwner(ctx context.Context, owner domain.ConversationOwner, requestID string, response ports.ChatInputResponse) error {
	controller, err := s.ControllerForOwner(owner)
	if err != nil {
		return err
	}
	return controller.ResolveInput(ctx, requestID, response)
}

// Interrupt cancels a session's in-flight turn.
func (s *Service) Interrupt(ctx context.Context, id domain.SessionID) error {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return err
	}
	return controller.Interrupt(ctx)
}

// InterruptForOwner cancels the in-flight turn for an owner-specific controller.
func (s *Service) InterruptForOwner(ctx context.Context, owner domain.ConversationOwner) error {
	controller, err := s.ControllerForOwner(owner)
	if err != nil {
		return err
	}
	return controller.Interrupt(ctx)
}

// ArmChatHandoff closes source intake and queue dispatch at
// interface-transition acceptance time. It is a reversible fence; durable queue
// settlement waits until target preflight succeeds.
func (s *Service) ArmChatHandoff(
	ctx context.Context,
	id domain.SessionID,
	policy domain.SessionInterfaceTransitionPolicy,
) error {
	controller, err := s.Controller(id)
	if errors.Is(err, ErrNoController) {
		return nil
	}
	if err != nil {
		return err
	}
	if controller.State() == ports.ChatControllerStopped {
		return nil
	}
	return controller.ArmHandoff(ctx, policy)
}

// PrepareChatHandoff closes source intake and makes the controller quiescent.
// Session Manager remains responsible for stopping it and starting the target;
// keeping that sequencing outside this package preserves the one-writer rule.
func (s *Service) PrepareChatHandoff(
	ctx context.Context,
	id domain.SessionID,
	policy domain.SessionInterfaceTransitionPolicy,
) error {
	controller, err := s.Controller(id)
	if errors.Is(err, ErrNoController) {
		// A dead/missing source controller is already quiescent. Session Manager
		// can safely stop (a no-op) and resume the native conversation in TUI,
		// which is also the useful recovery path for a broken Chat process.
		return nil
	}
	if err != nil {
		return err
	}
	if controller.State() == ports.ChatControllerStopped {
		return nil
	}
	if err := controller.BeginHandoff(ctx, policy); err != nil {
		return err
	}
	s.mu.Lock()
	if turn := controller.HandoffInterruptedTurn(); turn != "" {
		s.handoffInterrupted[id] = turn
	} else {
		delete(s.handoffInterrupted, id)
	}
	s.mu.Unlock()
	return nil
}

// AbortChatHandoff reopens the existing controller when the user cancels before
// Session Manager has stopped it.
func (s *Service) AbortChatHandoff(id domain.SessionID) {
	s.mu.Lock()
	delete(s.handoffInterrupted, id)
	s.mu.Unlock()
	controller, err := s.Controller(id)
	if err == nil {
		controller.AbortHandoff()
	}
}
