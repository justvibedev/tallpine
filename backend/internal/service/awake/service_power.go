package awake

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ObserveStatus refreshes settings-page observations at a bounded rate.
func (s *Service) ObserveStatus(ctx context.Context) domain.AwakeState {
	s.mu.Lock()
	if !s.closed && (s.observation.ObservedAt.IsZero() || s.now().Sub(s.observation.ObservedAt) >= 4*time.Second) {
		s.refresh(ctx)
	}
	s.mu.Unlock()
	return s.Status()
}

// finishPowerAuthorization refreshes permissions without holding the policy lock
// across a macOS administrator dialog.
func (s *Service) finishPowerAuthorization(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.powerAuthorizationBusy = false
	if !s.closed {
		s.refresh(ctx)
	}
}

// AuthorizeLowPower requests explicit macOS consent without holding the policy lock.
func (s *Service) AuthorizeLowPower(ctx context.Context) error {
	authorizer, ok := s.host.(interface{ AuthorizeLowPower(context.Context) error })
	if !ok {
		return invalid("this host cannot authorize Low Power Mode")
	}
	s.mu.Lock()
	if s.closed || s.powerAuthorizationBusy {
		s.mu.Unlock()
		return invalid("stay awake is shutting down or a power authorization action is in progress")
	}
	s.powerAuthorizationBusy = true
	s.mu.Unlock()
	defer s.finishPowerAuthorization(ctx)
	return authorizer.AuthorizeLowPower(ctx)
}

// RevokeLowPower disables automation durably before restoring owned changes and
// removing its account-specific grant. Cancellation never silently re-enables it.
func (s *Service) RevokeLowPower(ctx context.Context) error {
	revoker, ok := s.host.(interface{ RevokeLowPower(context.Context) error })
	if !ok {
		return invalid("this host cannot revoke Low Power Mode authorization")
	}
	s.mu.Lock()
	if s.closed || s.powerAuthorizationBusy {
		s.mu.Unlock()
		return invalid("stay awake is shutting down or a power authorization action is in progress")
	}
	previous := clone(s.doc)
	s.doc.Settings.LowPowerBatteryPercent = 0
	s.doc.Settings.LowPowerOnSeriousHeat = false
	if err := s.save(ctx); err != nil {
		s.doc = previous
		s.mu.Unlock()
		return err
	}
	if err := s.reconcile(ctx); err != nil {
		s.mu.Unlock()
		return err
	}
	s.powerAuthorizationBusy = true
	s.mu.Unlock()
	defer s.finishPowerAuthorization(ctx)
	if err := s.host.RequestRecovery(); err != nil {
		return err
	}
	return revoker.RevokeLowPower(ctx)
}
