package awake

import (
	"context"
	"slices"
	"time"
)

func (s *Service) reconcile(ctx context.Context) error {
	now := s.now()
	changed := s.reconcileSavePending
	count := 0
	unknownBinding := false
	lid := false
	// One native lease carries every contribution. Bound the entire aggregate
	// by its earliest finite deadline so independent sequence renewal cannot
	// preserve stale lid permission or policy effects from a shorter owner.
	var earliest time.Time
	for i := range s.doc.Sessions {
		source := &s.doc.Sessions[i]
		if source.EndedAt != nil {
			continue
		}
		reason := ""
		if deadline, ok := s.deadlines[source.ID]; ok && !now.Before(deadline) {
			reason = "expired"
		}
		if reason == "" && source.Scope == "session" {
			alive, _, err := s.bindings.Alive(ctx, "session", source.SessionID, "")
			if err != nil {
				unknownBinding = true
				continue
			}
			if !alive {
				reason = "session_ended"
			}
		}
		if reason == "" && source.Mode == "app" {
			alive, _, err := s.bindings.Alive(ctx, "app", source.BoundID, source.BoundIdentity)
			if err != nil {
				unknownBinding = true
				continue
			}
			if !alive {
				reason = "app_ended"
			}
		}
		if reason == "" && source.Mode == "download" && !s.downloadActive(source.BoundID, now) {
			reason = "download_ended"
			if s.downloadsSeen.IsZero() || now.Sub(s.downloadsSeen) >= 20*time.Second {
				reason = "download_observation_expired"
			}
		}
		if reason != "" {
			source.EndedAt = &now
			source.EndReason = reason
			changed = true
			continue
		}
		count++
		lid = lid || source.LidClosed
		deadline, finite := s.deadlines[source.ID]
		bound := source.Scope == "session" || source.Mode == "app" || source.Mode == "download"
		if bound {
			fresh := now.Add(15 * time.Second)
			if !finite || fresh.Before(deadline) {
				deadline = fresh
			}
			finite = true
		}
		if finite && (earliest.IsZero() || deadline.Before(earliest)) {
			earliest = deadline
		}
	}
	previousRules := s.activeRules
	s.activeRules = []string{}
	for i := range s.doc.Rules {
		r := &s.doc.Rules[i]
		matched := s.matchRuleActive(ctx, *r, slices.Contains(previousRules, r.ID))
		stable := s.stableRule(r.ID, matched, slices.Contains(previousRules, r.ID), now)
		if matched == 0 && r.Suppressed {
			r.Suppressed = false
			changed = true
		}
		if r.Enabled && !s.doc.Paused && !r.Suppressed && stable {
			s.activeRules = append(s.activeRules, r.ID)
			count++
			lid = lid || r.LidClosed
			fresh := now.Add(15 * time.Second)
			if earliest.IsZero() || fresh.Before(earliest) {
				earliest = fresh
			}
		}
	}
	_, _, nativeReason := s.host.LidStatus()
	reason := s.safety()
	if reason == "" && (nativeReason == "critical_heat" || nativeReason == "low_battery" || nativeReason == "battery_unknown") {
		reason = nativeReason
	}
	if reason != "" {
		changed = changed || s.doc.StopReason != reason || !s.doc.Paused
		s.doc.Paused = true
		s.doc.StopReason = reason
	}
	if changed {
		if err := s.save(ctx); err != nil {
			s.reconcileSavePending = true
			s.host.Stop()
			s.reason = "persistence_failed"
			return err
		}
	}
	if s.doc.StopReason != "" || count == 0 {
		if s.agentCancel != nil {
			s.agentCancel()
		}
		if s.networkCancel != nil {
			s.networkCancel()
		}
		s.activeRules = []string{}
		s.host.Stop()
		s.started = nil
		s.reason = s.doc.StopReason
		if s.reason == "" && unknownBinding {
			s.reason = "observation_unavailable"
		}
		return nil
	}
	if err := s.host.ConfigureEffects(s.doc.Settings); err != nil {
		return err
	}
	if err := s.host.SetProtection(s.doc.Settings.StopBatteryPercent); err != nil {
		return err
	}
	if err := s.host.ConfigureIdle(earliest, s.doc.Settings.DisplayAwake); err != nil {
		s.reason = "native_failed"
		return err
	}
	if err := s.host.SetDeadline(earliest); err != nil {
		s.reason = "lease_failed"
		return err
	}
	if err := s.host.ConfigureLid(lid, false); err != nil {
		s.reason = "native_failed"
		return err
	}
	if err := s.host.Start(); err != nil {
		s.reason = "native_failed"
		return err
	}
	if s.started == nil {
		s.started = &now
	}
	s.reason = ""
	return nil
}

// Run reconciles requests until shutdown.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	if s.needsObservations() {
		s.refresh(ctx)
	}
	_ = s.reconcile(ctx)
	s.mu.Unlock()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.closed = true
			s.host.Stop()
			now := s.now()
			for i := range s.doc.Sessions {
				if s.doc.Sessions[i].EndedAt == nil {
					s.doc.Sessions[i].EndedAt = &now
					s.doc.Sessions[i].EndReason = "shutdown"
				}
			}
			saveCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = s.save(saveCtx)
			cancel()
			s.mu.Unlock()
			s.workers.Wait()
			return
		case <-ticker.C:
			s.mu.Lock()
			if s.needsObservations() {
				s.refresh(ctx)
			}
			_ = s.reconcile(ctx)
			task := s.networkTask(ctx)
			agent := s.agentTask(ctx)
			s.mu.Unlock()
			if agent != nil {
				s.workers.Add(1)
				go func() { defer s.workers.Done(); s.runAgent(ctx, *agent) }()
			}
			if task != nil {
				s.workers.Add(1)
				go func() { defer s.workers.Done(); s.runNetwork(ctx, *task) }()
			}
		}
	}
}

// Recover ends requests and restores journaled effects.
func (s *Service) Recover(ctx context.Context) error {
	if err := s.StopAll(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.host.RequestRecovery()
}
