package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// CloseIndependentSideChat stops and removes one side chat.
func (s *Service) CloseIndependentSideChat(ctx context.Context, session domain.SessionID, sideID string) error {
	if s.sides == nil {
		return ErrSideUnavailable
	}
	owned, err := s.sides.ownedSide(ctx, session, sideID)
	if err != nil {
		return err
	}
	return s.closeSide(ctx, owned)
}

func (s *Service) closeSide(ctx context.Context, owned domain.SideConversation) error {
	sideID := owned.ID
	s.sides.mu.Lock()
	side, closeErr := s.sides.store.CloseSideConversation(ctx, sideID, s.now())
	if closeErr != nil {
		s.sides.mu.Unlock()
		return closeErr
	}
	if cancel := s.sides.restoring[sideID]; cancel != nil {
		cancel()
	}
	runtime := s.sides.runtimes[sideID]
	delete(s.sides.runtimes, sideID)
	s.sides.mu.Unlock()
	if runtime != nil {
		runtime.cancel()
		_ = runtime.conv.Interrupt(ctx, "")
		_ = runtime.conv.Close()
	}
	if s.stopProviderHost != nil {
		if err := s.stopProviderHost(ctx, domain.SessionID(owned.ProviderHostID)); err != nil {
			return fmt.Errorf("stop side provider host: %w", err)
		}
	}
	s.sides.store.CompleteSideHostCleanup(ctx, sideID)
	s.sides.announce(sideID)
	s.sides.deleteRegisteredFork(ctx, side, side.ProviderForkID)
	s.sides.signal()
	return nil
}

func (m *sideManager) deleteRegisteredFork(ctx context.Context, side domain.SideConversation, id string) {
	if id == "" {
		return
	}
	source, err := m.service.Controller(side.SessionID)
	if err != nil {
		return
	}
	if side.Harness != "" && source.harness != side.Harness {
		return
	}
	deleter, ok := source.conv.(ports.ChatForkDeleter)
	if !ok {
		if err := m.removeForkRecord(ctx, side, id); err == nil {
			_ = m.store.CompleteSideProviderCleanup(ctx, id)
		}
		return
	}
	if err := deleter.DeleteFork(ctx, id); err != nil {
		if errors.Is(err, ports.ErrChatCapabilityUnavailable) {
			if err := m.removeForkRecord(ctx, side, id); err == nil {
				_ = m.store.CompleteSideProviderCleanup(ctx, id)
			}
			return
		}
		m.service.log.Warn("side provider cleanup deferred", "side", side.ID, "error", err)
		return
	}
	if err := m.removeForkRecord(ctx, side, id); err != nil {
		m.service.log.Warn("side fork cleanup record removal failed", "side", side.ID, "error", err)
		return
	}
	_ = m.store.CompleteSideProviderCleanup(ctx, id)
}

func (m *sideManager) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	m.retryCleanup()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.retryCleanup()
		}
	}
}

func (m *sideManager) retryCleanup() {
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	defer cancel()
	sides, listErr := m.store.ListOpenSidesForRun(ctx, m.launchID())
	if listErr == nil && m.service.sessions != nil {
		for _, side := range sides {
			record, found, err := m.service.sessions.GetSession(ctx, side.SessionID)
			if err == nil && (!found || record.IsTerminated || domain.NormalizeSessionMode(record.Mode) != domain.SessionModeChat) {
				_ = m.service.closeSide(ctx, side)
			}
		}
	}
	for _, side := range m.store.SideHostCleanupPending(ctx) {
		if m.service.stopProviderHost != nil {
			if err := m.service.stopProviderHost(ctx, domain.SessionID(side.ProviderHostID)); err != nil {
				continue
			}
		}
		m.store.CompleteSideHostCleanup(ctx, side.ID)
	}
	items, err := m.store.SideProviderCleanupPending(ctx)
	if err != nil {
		m.service.log.Warn("side provider cleanup query failed", "error", err)
		return
	}
	for _, item := range items {
		m.deleteRegisteredFork(ctx, domain.SideConversation{SessionID: item.SessionID, Harness: item.Harness}, item.ForkID)
	}
}
