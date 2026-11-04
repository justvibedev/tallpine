package awake

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// RelaunchBrowser journals intent before the external action. Replaying an
// uncertain request never closes the browser a second time automatically.
func (s *Service) RelaunchBrowser(ctx context.Context, id, browser string, confirmed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	if _, err := uuid.Parse(id); err != nil {
		return invalid("browser action ID is required")
	}
	if !confirmed || !s.doc.Settings.BrowserActions {
		return invalid("enable browser actions and confirm closing its windows first")
	}
	for _, attempt := range s.doc.BrowserAttempts {
		if attempt.ID == id {
			if attempt.BrowserID != browser {
				return invalid("browser request ID was already used")
			}
			if attempt.Outcome == "completed" {
				return nil
			}
			return errors.New("this browser action is uncertain or failed; inspect the browser before making a new confirmed request")
		}
	}
	s.refresh(ctx)
	found := false
	for _, entry := range s.observation.Browsers {
		if entry.ID == browser {
			found = true
		}
	}
	if !found {
		return invalid("select an installed, signed supported browser")
	}
	if len(s.doc.BrowserAttempts) >= 1000 {
		return invalid("browser action history is full; disable this feature and contact support before issuing more actions")
	}
	previous := clone(s.doc)
	s.doc.BrowserAttempts = append(s.doc.BrowserAttempts, domain.AwakeBrowserAttempt{ID: id, BrowserID: browser, Outcome: "prepared"})
	if err := s.save(ctx); err != nil {
		s.doc = previous
		return err
	}
	s.workers.Add(1)
	s.mu.Unlock()
	result := s.host.RelaunchBrowser(ctx, browser)
	s.mu.Lock()
	s.workers.Done()
	for i := range s.doc.BrowserAttempts {
		if s.doc.BrowserAttempts[i].ID == id {
			s.doc.BrowserAttempts[i].Outcome = "uncertain"
			if result == nil {
				s.doc.BrowserAttempts[i].Outcome = "completed"
			}
		}
	}
	return errors.Join(result, s.save(ctx))
}

type networkTask struct {
	episode, ssid string
	ctx           context.Context
}

func (s *Service) networkTask(ctx context.Context) *networkTask {
	if ctx.Err() != nil || s.closed || s.networkBusy || !s.doc.Settings.HotspotRecovery || !s.host.Active() {
		return nil
	}
	connected := s.observation.NetworkConnected
	if connected == nil {
		return nil
	}
	if *connected {
		previous := s.doc.Network
		if previous.Episode != "" || previous.Attempts != 0 || previous.LastAttempt != nil {
			// Closing an outage must be durable before a later outage can use
			// a fresh retry budget. Keep the last action outcome for status.
			s.doc.Network = domain.AwakeNetworkAttempt{Outcome: previous.Outcome}
			if err := s.save(ctx); err != nil {
				s.doc.Network = previous
				s.reason = "persistence_failed"
				return nil
			}
			if s.reason == "persistence_failed" {
				s.reason = ""
			}
		}
		s.networkOutage = time.Time{}
		return nil
	}
	now := s.now()
	if s.networkOutage.IsZero() {
		s.networkOutage = now
		return nil
	}
	if now.Sub(s.networkOutage) < 10*time.Second || s.doc.Network.Attempts >= 3 {
		return nil
	}
	if last := s.doc.Network.LastAttempt; last != nil && now.Sub(*last) < 30*time.Second {
		return nil
	}
	previous := s.doc.Network
	if s.doc.Network.Episode == "" {
		s.doc.Network.Episode = uuid.NewString()
	}
	s.doc.Network.Attempts++
	s.doc.Network.LastAttempt = &now
	s.doc.Network.Outcome = "pending"
	if err := s.save(ctx); err != nil {
		s.doc.Network = previous
		return nil
	}
	s.networkBusy = true
	actionCtx, cancel := context.WithCancel(ctx)
	s.networkCancel = cancel
	return &networkTask{episode: s.doc.Network.Episode, ssid: s.doc.Settings.HotspotName, ctx: actionCtx}
}
func (s *Service) runNetwork(ctx context.Context, task networkTask) {
	result := s.host.JoinNetwork(task.ctx, task.ssid)
	cancelled := task.ctx.Err() != nil
	s.mu.Lock()
	defer s.mu.Unlock()
	s.networkBusy = false
	if s.networkCancel != nil {
		s.networkCancel()
		s.networkCancel = nil
	}
	if s.doc.Network.Episode != task.episode {
		return
	}
	s.doc.Network.Outcome = "completed"
	if result != nil {
		s.doc.Network.Outcome = "failed"
		if cancelled {
			s.doc.Network.Outcome = "cancelled"
		}
	}
	if err := s.save(ctx); err != nil {
		s.doc.Network.Outcome = "uncertain"
	}
}

const maxAwakeSessions = 1000

// pruneEndedSessions drops the oldest ended sessions once the document is at
// capacity, so history never blocks a new request. Only active sessions count
// toward the limit.
func (s *Service) pruneEndedSessions() {
	excess := len(s.doc.Sessions) - maxAwakeSessions + 1
	if excess <= 0 {
		return
	}
	kept := make([]domain.AwakeSession, 0, len(s.doc.Sessions))
	for _, source := range s.doc.Sessions {
		if excess > 0 && source.EndedAt != nil {
			excess--
			continue
		}
		kept = append(kept, source)
	}
	s.doc.Sessions = kept
}
