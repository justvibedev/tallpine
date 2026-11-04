package awake

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Start starts a wake request.
func (s *Service) Start(ctx context.Context, req domain.StartAwakeRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	if !s.host.Supported() {
		return errors.New("stay awake requires macOS")
	}
	s.refresh(ctx)
	if s.doc.StopReason != "" {
		return invalid("resume after the safety stop before starting another session")
	}
	if req.Scope == "" {
		req.Scope = "ao"
	}
	if req.Mode == "" {
		req.Mode = "indefinite"
	}
	if len(req.SessionID) > 256 || len(req.BoundID) > 256 || len(req.BoundIdentity) > 128 {
		return invalid("awake binding is too long")
	}
	if req.Scope == "ao" {
		req.SessionID = ""
	}
	if req.Scope != "ao" && req.Scope != "session" {
		return invalid("choose Tallpine or this session")
	}
	if req.Scope == "session" && req.SessionID == "" {
		return invalid("session is required")
	}
	if req.Scope == "session" {
		ok, _, err := s.bindings.Alive(ctx, "session", req.SessionID, "")
		if err != nil {
			return err
		}
		if !ok {
			return invalid("session is no longer running")
		}
	}
	now := s.now()
	source := domain.AwakeSession{ID: uuid.NewString(), Scope: req.Scope, SessionID: req.SessionID, Mode: req.Mode, CreatedAt: now, Label: "For Tallpine", LidClosed: req.LidClosed, BoundID: req.BoundID}
	if req.Scope == "session" {
		source.Label = "For this session"
	}
	switch req.Mode {
	case "indefinite":
	case "duration":
		if req.DurationSeconds < 1 || req.DurationSeconds > 604800 {
			return invalid("duration must be between one second and seven days")
		}
		deadline := now.Add(time.Duration(req.DurationSeconds) * time.Second)
		source.Deadline = &deadline
	case "until":
		if req.Until == nil || !req.Until.After(now) || req.Until.Sub(now) > 7*24*time.Hour {
			return invalid("choose an end time within seven days")
		}
		deadline := now.Add(req.Until.Sub(now))
		source.Deadline = &deadline
	case "app":
		if req.BoundID == "" {
			return invalid("choose a running application")
		}
		alive, identity, err := s.bindings.Alive(ctx, "app", req.BoundID, req.BoundIdentity)
		if err != nil {
			return err
		}
		if !alive {
			return invalid("application is not running")
		}
		source.BoundIdentity = identity
	case "download":
		if !s.downloadActive(req.BoundID, now) {
			return invalid("choose an active Tallpine browser download")
		}
	default:
		return invalid("unknown awake session mode")
	}
	s.pruneEndedSessions()
	if len(s.doc.Sessions) >= maxAwakeSessions {
		return invalid("too many active awake sessions; end some first")
	}
	nativeGuard := false
	for _, capab := range s.host.Capabilities() {
		if capab.ID == "native_guard" {
			nativeGuard = capab.Available
		}
	}
	if req.LidClosed {
		supported, _, reason := s.host.LidStatus()
		if !supported {
			return fmt.Errorf("closed-lid wake is unavailable: %s", reason)
		}
	}
	if req.LidClosed || nativeGuard {
		if err := s.host.SetupLid(); err != nil && (req.LidClosed || !errors.Is(err, ErrLidHelperUpgrade)) {
			return err
		}
	}
	previous := clone(s.doc)
	// A new explicit request can rearm only after fresh observations are safe.
	s.refresh(ctx)
	if reason := s.safety(); reason != "" {
		return fmt.Errorf("stay awake stopped: %s", reason)
	}
	s.doc.Sessions = append(s.doc.Sessions, source)
	if source.Deadline != nil {
		s.deadlines[source.ID] = *source.Deadline
	}
	if err := s.save(ctx); err != nil {
		s.doc = previous
		delete(s.deadlines, source.ID)
		return err
	}
	if err := s.reconcile(ctx); err != nil {
		s.doc = previous
		delete(s.deadlines, source.ID)
		rollback := s.save(ctx)
		_ = s.reconcile(ctx)
		return errors.Join(err, rollback)
	}
	return nil
}

// End ends one owned request.
func (s *Service) End(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	previous := clone(s.doc)
	found := false
	for i := range s.doc.Sessions {
		source := &s.doc.Sessions[i]
		if source.ID == id && source.EndedAt == nil {
			now := s.now()
			source.EndedAt = &now
			source.EndReason = "ended"
			found = true
		}
	}
	if !found {
		return invalid("awake session not found")
	}
	if err := s.save(ctx); err != nil {
		s.doc = previous
		return err
	}
	return s.reconcile(ctx)
}

// Extend extends a finite request.
func (s *Service) Extend(ctx context.Context, id string, seconds int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	if seconds < 1 || seconds > 604800 {
		return invalid("extension must be within seven days")
	}
	for i := range s.doc.Sessions {
		source := &s.doc.Sessions[i]
		if source.ID != id || source.EndedAt != nil {
			continue
		}
		original, finite := s.deadlines[id]
		if !finite || !original.After(s.now()) {
			return invalid("only an unexpired timed session can be extended")
		}
		previous := clone(s.doc)
		base := s.now()
		if old, ok := s.deadlines[id]; ok && old.After(base) {
			base = old
		}
		end := base.Add(time.Duration(seconds) * time.Second)
		if end.Sub(s.now()) > 7*24*time.Hour {
			return invalid("maximum duration is seven days")
		}
		source.Deadline = &end
		source.Mode = "duration"
		if err := s.save(ctx); err != nil {
			s.doc = previous
			return err
		}
		s.deadlines[id] = end
		return s.reconcile(ctx)
	}
	return invalid("awake session not found")
}

// StopAll ends desktop requests and pauses rules.
func (s *Service) StopAll(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	previous := clone(s.doc)
	now := s.now()
	for i := range s.doc.Sessions {
		if s.doc.Sessions[i].EndedAt == nil {
			s.doc.Sessions[i].EndedAt = &now
			s.doc.Sessions[i].EndReason = "stopped"
		}
	}
	s.doc.Paused = true
	if err := s.save(ctx); err != nil {
		s.doc = previous
		return err
	}
	return s.reconcile(ctx)
}

// Resume checks fresh host readings before clearing a safety stop.
func (s *Service) Resume(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	s.refresh(ctx)
	if reason := s.safety(); reason != "" {
		return fmt.Errorf("cannot rearm: %s", reason)
	}
	if s.doc.StopReason != "" && (s.observation.Thermal == "" || s.observation.Thermal == "unknown" || s.observation.OnBattery == nil || (*s.observation.OnBattery && s.observation.BatteryPercent == nil)) {
		return errors.New("fresh battery and thermal observations are required to rearm")
	}

	previous := clone(s.doc)
	s.doc.Paused = false
	s.doc.StopReason = ""
	if err := s.save(ctx); err != nil {
		s.doc = previous
		return err
	}
	return s.reconcile(ctx)
}

// clearDeferredSettings returns whether a previous build enabled a policy that
// is outside this release's supported surface. Low Power Mode and display wake
// assertions remain supported; optional brightness/audio/app/network effects do not.
func clearDeferredSettings(settings *domain.AwakeSettings) bool {
	changed := settings.MuteAudio || settings.DarkenKeyboard || settings.RestoreDisplay ||
		len(settings.PauseApps) != 0 || settings.HotspotName != "" || settings.HotspotRecovery ||
		settings.AgentReconnect || settings.BrowserActions
	settings.MuteAudio, settings.DarkenKeyboard, settings.RestoreDisplay = false, false, false
	settings.PauseApps = []string{}
	settings.HotspotName, settings.HotspotRecovery = "", false
	settings.AgentReconnect, settings.BrowserActions = false, false
	return changed
}
