package chat

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// UsageLimitPausedUntil reports when a live session whose provider quota is
// exhausted may start turns again. It reads the durable turn, so it survives a
// restart; a session with no running controller reports no pause.
func (s *Service) UsageLimitPausedUntil(ctx context.Context, id domain.SessionID) (time.Time, bool) {
	controller, err := s.Controller(id)
	if err != nil || controller == nil {
		return time.Time{}, false
	}
	return controller.usageLimitPausedUntil(ctx)
}

// wakeUsageLimitPaused re-checks every other controller of the same harness that
// is waiting out a usage-limit pause. A turn that just completed may prove their
// shared account usable again; a controller still paused re-arms its own wake.
func (s *Service) wakeUsageLimitPaused(from domain.SessionID, harness domain.AgentHarness) {
	s.mu.RLock()
	var waiting []*Controller
	for id, controller := range s.controllers {
		if id == from || controller.harness != harness {
			continue
		}
		controller.mu.Lock()
		armed := controller.pauseWake != nil
		controller.mu.Unlock()
		if armed {
			waiting = append(waiting, controller)
		}
	}
	s.mu.RUnlock()
	for _, controller := range waiting {
		go controller.drainAfterPause()
	}
}
