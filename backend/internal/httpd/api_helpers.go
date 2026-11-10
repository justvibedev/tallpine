package httpd

import (
	"tallpine.eu/tallpine/backend/internal/attachmentstore"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
)

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

func newSessionsController(cfg config.Config, deps APIDeps) *controllers.SessionsController {
	return &controllers.SessionsController{
		Routes:                   routeNoticer(deps.Reports),
		OwnerMessages:            deps.OwnerMessages,
		Policy:                   policyDecider(deps.AgentPolicy),
		Svc:                      deps.Sessions,
		Backlog:                  deps.BacklogSpawner,
		PlannerEnabled:           func() bool { return deps.BacklogPlans != nil && deps.BacklogPlans.Enabled() },
		Projects:                 deps.Projects,
		Activity:                 deps.Activity,
		DataDir:                  cfg.DataDir,
		RunFilePath:              cfg.RunFilePath,
		Usage:                    deps.UsageHooks,
		Attachments:              attachmentstore.New(cfg.DataDir),
		PreviewServer:            deps.PreviewServer,
		Capabilities:             deps.SessionCapabilities,
		ArtifactVersions:         deps.ArtifactVersions,
		LastMessages:             deps.LastMessages,
		Models:                   deps.Models,
		ServedModel:              deps.ServedModel,
		Instructions:             deps.Instructions,
		BackgroundTasks:          deps.BackgroundTasks,
		UsageLimits:              deps.UsageLimits,
		MessageSent:              deps.MessageSent,
		ShellPreviewCapabilities: deps.ShellPreviewCapabilities,
		AgentBrowser:             agentBrowserURL(cfg.AgentBrowserURL),
		AgentBrowserToken:        cfg.AgentBrowserToken,
	}
}
