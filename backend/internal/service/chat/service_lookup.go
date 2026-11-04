package chat

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Controller returns a session's live controller.
func (s *Service) Controller(sessionID domain.SessionID) (*Controller, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	controller, ok := s.controllers[sessionID]
	if !ok {
		return nil, ErrNoController
	}
	return controller, nil
}

// ControllerForOwner returns a live controller using the typed durable owner.
func (s *Service) ControllerForOwner(owner domain.ConversationOwner) (*Controller, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	controller, ok := s.ownerControllers[owner]
	if !ok {
		return nil, ErrNoController
	}
	return controller, nil
}

// HasLiveChatController reports whether the service owns a controller that can
// still process provider events. A stopped controller can remain in the registry
// briefly while its final cleanup lands; Start waits for that cleanup before
// replacing it rather than treating the dead entry as a successful resume.
func (s *Service) HasLiveChatController(sessionID domain.SessionID) bool {
	s.mu.RLock()
	controller := s.controllers[sessionID]
	s.mu.RUnlock()
	return controller != nil && controller.State() != ports.ChatControllerStopped
}

// HasLiveControllerForOwner is the typed-owner form used by reviewer chats.
func (s *Service) HasLiveControllerForOwner(owner domain.ConversationOwner) bool {
	controller, err := s.ControllerForOwner(owner)
	return err == nil && controller.State() != ports.ChatControllerStopped
}

// PreservesProviderOnRestart reports only established live ownership. Unknown
// or recovering sessions remain conservative for the desktop update warning.
func (s *Service) PreservesProviderOnRestart(sessionID domain.SessionID) bool {
	controller, err := s.Controller(sessionID)
	if err != nil || controller.State() == ports.ChatControllerStopped {
		return false
	}
	preserver, ok := controller.conv.(ports.ChatProviderPreserver)
	return ok && preserver.PreservesProviderOnClose()
}

// requireChatSession reads the persisted mode and refuses anything that is not a
// Chat session. Dispatch is decided by durable state, never by the caller.
func (s *Service) requireChatSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	record, found, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("read session %s: %w", id, err)
	}
	if !found {
		return domain.SessionRecord{}, ports.ErrSessionNotFound
	}
	if domain.NormalizeSessionMode(record.Mode) != domain.SessionModeChat {
		return domain.SessionRecord{}, ErrNotChatMode
	}
	return record, nil
}
