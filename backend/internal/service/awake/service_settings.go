package awake

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// UpdateSettings validates and persists host policy.
func (s *Service) UpdateSettings(ctx context.Context, settings domain.AwakeSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	if s.powerAuthorizationBusy && (settings.LowPowerBatteryPercent != 0 || settings.LowPowerOnSeriousHeat) {
		return invalid("wait for the Low Power Mode authorization action to finish")
	}
	supported := settings
	if clearDeferredSettings(&supported) {
		return invalid("App pausing, display/keyboard/audio effects, network recovery, agent reconnect and browser actions are deferred to a follow-up release")
	}
	if settings.LowPowerBatteryPercent != 0 && (settings.LowPowerBatteryPercent < 10 || settings.LowPowerBatteryPercent > 90) {
		return invalid("Low Power Mode threshold must be between 10 and 90 percent")
	}
	if settings.StopBatteryPercent < 10 || settings.StopBatteryPercent > 90 {
		return invalid("battery stop threshold must be between 10 and 90 percent")
	}
	// An unavailable operation is rejected rather than saved as if effective.
	if len(settings.PauseApps) > 20 {
		return invalid("choose at most twenty background apps")
	}
	for _, app := range settings.PauseApps {
		if app == "" || len(app) > 256 {
			return invalid("invalid application bundle ID")
		}
	}
	addedApps := false
	for _, app := range settings.PauseApps {
		if !slices.Contains(s.doc.Settings.PauseApps, app) {
			addedApps = true
		}
	}
	requested := map[string]bool{"lid": settings.LidClosed && !s.doc.Settings.LidClosed, "display_awake": settings.DisplayAwake && !s.doc.Settings.DisplayAwake, "low_power": (settings.LowPowerBatteryPercent > 0 && settings.LowPowerBatteryPercent != s.doc.Settings.LowPowerBatteryPercent) || (settings.LowPowerOnSeriousHeat && !s.doc.Settings.LowPowerOnSeriousHeat), "keyboard": (settings.DarkenKeyboard && !s.doc.Settings.DarkenKeyboard), "display_restore": (settings.RestoreDisplay && !s.doc.Settings.RestoreDisplay), "audio": (settings.MuteAudio && !s.doc.Settings.MuteAudio), "pause_apps": addedApps, "hotspot": (settings.HotspotRecovery && !s.doc.Settings.HotspotRecovery), "agent_reconnect": (settings.AgentReconnect && !s.doc.Settings.AgentReconnect), "browser_actions": settings.BrowserActions && !s.doc.Settings.BrowserActions}
	for id, needed := range requested {
		if !needed {
			continue
		}
		available := false
		reason := "this host does not support " + id
		for _, capability := range s.capabilities() {
			if capability.ID == id {
				available = capability.Available
				if capability.Reason != "" {
					reason = capability.Reason
				}
			}
		}
		if !available {
			return invalid(reason)
		}
	}
	if settings.HotspotRecovery {
		if settings.HotspotName == "" || !slices.Contains(s.observation.KnownNetworks, settings.HotspotName) {
			return invalid("select a saved macOS Wi-Fi network first")
		}
	}
	previous := clone(s.doc)
	if settings.HotspotRecovery && !s.doc.Settings.HotspotRecovery {
		s.doc.Network = domain.AwakeNetworkAttempt{}
	}
	s.doc.Settings = settings
	if err := s.save(ctx); err != nil {
		s.doc = previous
		return err
	}
	if !settings.AgentReconnect && s.agentCancel != nil {
		s.agentCancel()
	}
	if !settings.HotspotRecovery && s.networkCancel != nil {
		s.networkCancel()
	}
	return s.reconcile(ctx)
}

// UpdateRules validates and persists activation rules.
func (s *Service) UpdateRules(ctx context.Context, rules []domain.AwakeRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	if len(rules) > 50 {
		return invalid("at most fifty automatic rules")
	}
	seen := map[string]bool{}
	for i := range rules {
		r := &rules[i]
		if r.Enabled && !s.host.Supported() {
			return invalid("Stay Awake requires macOS")
		}
		if r.ID == "" {
			r.ID = uuid.NewString()
		}
		if seen[r.ID] {
			return invalid("duplicate rule ID")
		}
		seen[r.ID] = true
		if err := validateRule(*r); err != nil {
			return err
		}
		guard := false
		for _, capab := range s.host.Capabilities() {
			if capab.ID == "native_guard" {
				guard = capab.Available
			}
		}
		if r.Enabled && (r.LidClosed || guard) {
			if err := s.host.SetupLid(); err != nil && (r.LidClosed || !errors.Is(err, ErrLidHelperUpgrade)) {
				return err
			}
		}
	}
	previous := clone(s.doc)
	s.doc.Rules = rules
	s.ruleMatches = map[string]ruleMatch{}
	if err := s.save(ctx); err != nil {
		s.doc = previous
		return err
	}
	s.refresh(ctx)
	return s.reconcile(ctx)
}

// Downloads updates typed download heartbeats.
func (s *Service) Downloads(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	if len(ids) > 200 {
		return invalid("too many downloads")
	}
	next := map[string]bool{}
	for _, id := range ids {
		if id == "" || len(id) > 128 {
			return invalid("invalid download ID")
		}
		next[id] = true
	}
	s.downloads = next
	s.downloadsSeen = s.now()
	return nil
}
func (s *Service) downloadActive(id string, now time.Time) bool {
	return !s.downloadsSeen.IsZero() && now.Sub(s.downloadsSeen) < 20*time.Second && ((id == "any" && len(s.downloads) > 0) || s.downloads[id])
}
func (s *Service) refresh(ctx context.Context) {
	observation, err := s.host.Observe(ctx)
	if err != nil {
		observation = domain.AwakeObservation{}
	}
	if active, err := s.bindings.AgentActive(ctx); err == nil {
		observation.AgentActive = &active
	}
	observation.ObservedAt = s.now()
	s.observation = observation
}
func (s *Service) safety() string {
	if s.observation.Thermal == "critical" {
		return "critical_heat"
	}
	if s.observation.OnBattery != nil && *s.observation.OnBattery && s.observation.BatteryPercent != nil && *s.observation.BatteryPercent < s.doc.Settings.StopBatteryPercent {
		return "low_battery"
	}
	return ""
}
