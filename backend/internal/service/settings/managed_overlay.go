package settings

import "encoding/json"

// retentionKey is the registry key of the provider-event retention window.
const retentionKey = "app.providerEventRetentionDays"

// applyManaged overlays the managed retention window on a stored snapshot.
func (s *Service) applyManaged(snap Snapshot) Snapshot {
	if days, ok := s.managedRetention(); ok {
		snap.ProviderEventRetentionDays = days
	}
	if on, ok := s.managedDynamicContext(); ok {
		snap.DynamicOrchestratorContext = on
	}
	return snap
}

// dynamicContextKey is the registry key of the dynamic orchestrator context toggle.
const dynamicContextKey = "app.dynamicOrchestratorContext"

// managedDynamicContext is the managed toggle value, when one is set and valid.
func (s *Service) managedDynamicContext() (on, ok bool) {
	v, locked := s.managed.Value(dynamicContextKey)
	if !locked || v == nil || json.Unmarshal(v, &on) != nil {
		return false, false
	}
	return on, true
}

// managedRetention is the managed retention window, when one is set and valid.
func (s *Service) managedRetention() (int, bool) {
	var days int
	v, locked := s.managed.Value(retentionKey)
	if !locked || v == nil || json.Unmarshal(v, &days) != nil ||
		days < MinProviderEventRetentionDays || days > MaxProviderEventRetentionDays {
		return 0, false
	}
	return days, true
}
