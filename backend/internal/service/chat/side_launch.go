package chat

import (
	"context"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// InitializeSideChats claims the desktop launch and restores only its sides.
// A new launch removes the old Tallpine transcript and drafts before creation opens.
func (s *Service) InitializeSideChats(ctx context.Context) error {
	if s.sides == nil {
		return nil
	}
	if err := s.sides.store.RecoverInterruptedSideTurns(ctx, s.sides.launchID(), s.now()); err != nil {
		return err
	}
	return s.sides.claim(ctx, s.sides.launchID())
}

// ClaimSideChatLaunch claims side chat ownership for the current desktop launch.
func (s *Service) ClaimSideChatLaunch(ctx context.Context, runID string) error {
	if s.sides == nil {
		return ErrSideUnavailable
	}
	if current := s.sides.launchID(); current != "" && current != runID {
		return ErrSideLaunchUnclaimed
	}
	return s.sides.claim(ctx, runID)
}

// ExportSideChatLaunch exports ephemeral side state for Electron recovery.
func (s *Service) ExportSideChatLaunch(_ context.Context, runID string) ([]SideRecoveryRecord, error) {
	if s.sides == nil || runID == "" || runID != s.sides.launchID() {
		return nil, ErrSideLaunchUnclaimed
	}
	store, ok := s.sides.store.(*memorySideStore)
	if !ok {
		return nil, ErrSideUnavailable
	}
	return store.export(runID), nil
}

// RetireSideChatLaunch stops and removes sides belonging to a desktop launch.
func (s *Service) RetireSideChatLaunch(ctx context.Context, runID string) error {
	if s.sides == nil || runID == "" || runID != s.sides.launchID() {
		return ErrSideLaunchUnclaimed
	}
	sides, err := s.sides.store.ListOpenSidesForRun(ctx, runID)
	if err != nil {
		return err
	}
	var failures []error
	for _, side := range sides {
		if err := s.CloseIndependentSideChat(ctx, side.SessionID, side.ID); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// RecoverSideChatLaunch restores Electron-held side state after a daemon restart.
func (s *Service) RecoverSideChatLaunch(ctx context.Context, runID string, records []SideRecoveryRecord) error {
	if s.sides == nil || runID == "" || runID != s.sides.launchID() {
		return ErrSideLaunchUnclaimed
	}
	for _, record := range records {
		if record.CleanupOnly {
			continue
		}
		if session, err := s.requireChatSession(ctx, record.Side.SessionID); err != nil {
			return err
		} else if session.IsTerminated {
			return ErrSideUnavailable
		}
		main, err := s.store.ConversationForSession(ctx, record.Side.SessionID)
		if err != nil {
			return err
		}
		if main.ID != record.Side.MainConversationID {
			return ErrSideUnavailable
		}
	}
	store, ok := s.sides.store.(*memorySideStore)
	if !ok {
		return ErrSideUnavailable
	}
	created, err := store.recover(runID, records, s.now())
	if err != nil {
		return err
	}
	for _, side := range created {
		if side.State == "failed" && side.ErrorMessage == ErrSidePolicyRecreate.Error() && s.stopProviderHost != nil {
			if err := s.stopProviderHost(ctx, domain.SessionID(side.ProviderHostID)); err != nil {
				return fmt.Errorf("stop side provider with an unverifiable boundary: %w", err)
			}
		}
		if side.State == "ready" || side.State == "recovering" {
			if err := s.sides.ensureRestore(side); err != nil {
				s.sides.failOpen(side, err)
			}
		}
	}
	return nil
}

func (m *sideManager) claim(ctx context.Context, runID string) error {
	if runID == "" {
		return ErrSideUnavailable
	}
	retired, err := m.store.ClaimSideLaunch(ctx, runID, m.service.now())
	if err != nil {
		return err
	}
	var retiredRuntimes []*sideRuntime
	m.mu.Lock()
	if !m.started {
		if err := m.recoverForkRecords(ctx, runID); err != nil {
			m.mu.Unlock()
			return fmt.Errorf("recover side fork cleanup: %w", err)
		}
	}
	m.runID = runID
	for _, side := range retired {
		if cancel := m.restoring[side.ID]; cancel != nil {
			cancel()
		}
		if runtime := m.runtimes[side.ID]; runtime != nil {
			runtime.cancel()
			retiredRuntimes = append(retiredRuntimes, runtime)
			delete(m.runtimes, side.ID)
		}
	}
	if !m.started {
		m.started = true
		go m.schedule()
		go m.cleanupLoop()
	}
	m.mu.Unlock()
	for _, runtime := range retiredRuntimes {
		_ = runtime.conv.Close()
	}
	for _, side := range retired {
		if m.service.stopProviderHost != nil {
			_ = m.service.stopProviderHost(ctx, domain.SessionID(side.ProviderHostID))
		}
		m.deleteRegisteredFork(ctx, side, side.ProviderForkID)
	}
	sides, err := m.store.ListOpenSidesForRun(ctx, runID)
	if err != nil {
		return err
	}
	for _, side := range sides {
		if side.State == "ready" || side.State == "recovering" {
			if err := m.ensureRestore(side); err != nil {
				m.failOpen(side, err)
			}
		}
		if side.State == "opening" {
			_ = m.store.SetSideFailed(ctx, side.ID, side.Generation, "Side opening was interrupted; close and reopen it.", m.service.now())
		}
	}
	return nil
}
