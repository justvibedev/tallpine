package httpd

import "tallpine.eu/tallpine/backend/internal/httpd/controllers"

// policyDecider returns the engine behind the agent policy service when it can
// decide per session, nil otherwise.
func policyDecider(svc controllers.AgentPolicyService) controllers.PolicyDecider {
	decider, _ := svc.(controllers.PolicyDecider)
	return decider
}

// newSettingsController lets a saved compaction default reach running chats when
// the Chat service is wired to apply it.
func newSettingsController(deps APIDeps) *controllers.SettingsController {
	controller := &controllers.SettingsController{Audit: deps.SettingsAudit, Svc: deps.Settings}
	if applier, ok := deps.Conversations.(controllers.CompactionDefaultApplier); ok {
		controller.Compaction = applier
	}
	return controller
}

// routeNoticer returns the report service's routing notice, when it has one.
func routeNoticer(s controllers.ReportService) controllers.RouteNoticer {
	rn, _ := s.(controllers.RouteNoticer)
	return rn
}

// projectCreators returns the store behind the scope gate when it remembers
// project creators, nil otherwise.
func projectCreators(store AgentScopeStore) controllers.ProjectCreatorStore {
	creators, _ := store.(controllers.ProjectCreatorStore)
	return creators
}
