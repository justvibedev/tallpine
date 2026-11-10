package controllers

import (
	"sort"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/systeminstall"
)

// SettingsResponse is the daemon-owned preference set.
type SettingsResponse struct {
	// HarnessDefaults are per-harness picker preferences, keyed by harness id.
	HarnessDefaults map[string]domain.HarnessDefault `json:"harnessDefaults,omitempty"`
	// DefaultSessionMode applies to sessions created from now on. Changing it
	// never alters an existing session; only an explicit interface transition can.
	DefaultSessionMode string `json:"defaultSessionMode" enum:"chat,tui"`
	// ChatHarnesses are the agents that can run in chat mode today. Empty means
	// chat cannot be used yet, which a client should say plainly.
	ChatHarnesses []string `json:"chatHarnesses"`
	// Client is the deployment's client identity (TALLPINE_CLIENT); empty when unset.
	Client string `json:"client"`
	// LocalEnabled reports whether the local offering is available.
	LocalEnabled bool `json:"localEnabled"`
	// ChatHibernationEnabled lets idle Chat sessions release their agent processes.
	ChatHibernationEnabled bool `json:"chatHibernationEnabled" description:"Idle Chat sessions stop their agent processes and resume on the next message, view or report."`
	// ChatHibernationWorkerIdleSeconds is how long a worker must be quiet before it sleeps.
	ChatHibernationWorkerIdleSeconds int64 `json:"chatHibernationWorkerIdleSeconds"`
	// ChatHibernationOrchestratorIdleSeconds is how long an orchestrator must be quiet before it sleeps.
	ChatHibernationOrchestratorIdleSeconds int64 `json:"chatHibernationOrchestratorIdleSeconds"`
	// VersionRefresh moves idle open sessions to a harness release installed while they ran.
	VersionRefresh bool `json:"versionRefresh" description:"After a harness update, idle open sessions restart on the new release instead of running the old one until they happen to restart."`
	// DynamicOrchestratorContext lets an orchestrator raise its compaction window while it researches.
	// WorkflowDefaults are the app-wide values a project inherits when it sets none (B525).
	WorkflowDefaults           WorkflowDefaultsResponse `json:"workflowDefaults"`
	DynamicOrchestratorContext bool                     `json:"dynamicOrchestratorContext" description:"Lets an orchestrator raise its compaction window (250k by default) while it examines a large codebase itself; plain coordination keeps the early window. Off ends every running research mode."`
	// StorageCompaction coalesces repeated activity writes and compacts and
	// expires the raw provider-event archive.
	StorageCompaction bool `json:"storageCompaction" description:"Skip activity writes that change nothing, batch streamed activity output, drop streamed deltas from the provider-event archive once their turn settles, and delete provider events of long-terminated sessions. Conversation history is unaffected."`
	// ProviderEventRetentionDays is how long a terminated session's provider
	// events are kept while StorageCompaction is on.
	ProviderEventRetentionDays int `json:"providerEventRetentionDays" minimum:"1" maximum:"3650"`
	// TrackerIntakeEnabled reports the TALLPINE_TRACKER_INTAKE gate, so a client can
	// avoid offering a per-project intake control the daemon will ignore.
	TrackerIntakeEnabled bool `json:"trackerIntakeEnabled"`
	// BacklogRunEnabled reports the TALLPINE_BACKLOG_RUN gate (backlog run mode).
	BacklogRunEnabled bool `json:"backlogRunEnabled"`
}

// AgentInstallerCatalogResponse is the body of GET /api/v1/agents/installers.
type AgentInstallerCatalogResponse struct {
	Agents []systeminstall.AgentPlan `json:"agents"`
}

// UpdateSessionInterfaceRequest changes the default interface for new sessions.
type UpdateSessionInterfaceRequest struct {
	DefaultSessionMode string `json:"defaultSessionMode" enum:"chat,tui"`
}

// UpdateHarnessDefaultRequest changes one harness's preferences. A field left
// out keeps its saved value; at least one must be present.
type UpdateHarnessDefaultRequest struct {
	// DisabledModels lists the model ids (each up to 256 characters) clients
	// should leave out of their pickers. An empty list offers every catalog model.
	DisabledModels *[]string `json:"disabledModels,omitempty" maxItems:"20000"`
	// AutoCompactWindow is where new sessions of this harness compact on their
	// own: a token preset or a percent of the model window. An empty string
	// clears it (Auto).
	AutoCompactWindow *string `json:"autoCompactWindow,omitempty" description:"Empty (Auto), a size from 50k to 1m (Claude Code runs anything under 100k as 100k), or a whole percent from 20% to 95% of the model window."`
}

// UpdateDynamicOrchestratorContextRequest flips whether orchestrators may use research mode.
type UpdateDynamicOrchestratorContextRequest struct {
	Enabled *bool `json:"enabled"`
}

// UpdateWorkflowDefaultsRequest changes the app-wide workflow defaults projects
// inherit. Omitted fields keep their value.
type UpdateWorkflowDefaultsRequest struct {
	AutoReview      *bool   `json:"autoReview,omitempty" description:"Auto-review for projects that set none of their own."`
	PlanningMode    *string `json:"planningMode,omitempty" description:"Planning mode (auto, always or off) for projects that set none of their own."`
	PlanAutoApprove *bool   `json:"planAutoApprove,omitempty" description:"Plan auto-approval for projects that set none of their own."`
	ReviewerHarness *string `json:"reviewerHarness,omitempty" description:"Default reviewer harness for projects that set no reviewer of their own; empty clears it and the default model."`
	ReviewerModel   *string `json:"reviewerModel,omitempty" description:"Default reviewer model paired with the default reviewer harness."`
}

// WorkflowDefaultsResponse is the app-wide workflow defaults.
type WorkflowDefaultsResponse struct {
	AutoReview      bool   `json:"autoReview"`
	PlanningMode    string `json:"planningMode"`
	PlanAutoApprove bool   `json:"planAutoApprove"`
	ReviewerHarness string `json:"reviewerHarness,omitempty"`
	ReviewerModel   string `json:"reviewerModel,omitempty"`
}

// UpdateVersionRefreshRequest flips whether open sessions move to new harness releases.
type UpdateVersionRefreshRequest struct {
	Enabled *bool `json:"enabled"`
}

// UpdateStorageCompactionRequest changes the storage-compaction toggle, the
// provider-event retention window, or both. Omitted fields keep their value.
type UpdateStorageCompactionRequest struct {
	Enabled                    *bool `json:"enabled,omitempty"`
	ProviderEventRetentionDays *int  `json:"providerEventRetentionDays,omitempty" minimum:"1" maximum:"3650"`
}

// UpdateChatHibernationRequest changes any subset of the idle Chat
// hibernation preference. Thresholds are whole seconds from 60 to 604800.
type UpdateChatHibernationRequest struct {
	Enabled                 *bool  `json:"enabled,omitempty"`
	WorkerIdleSeconds       *int64 `json:"workerIdleSeconds,omitempty" minimum:"60" maximum:"604800"`
	OrchestratorIdleSeconds *int64 `json:"orchestratorIdleSeconds,omitempty" minimum:"60" maximum:"604800"`
}

// capabilityNames lists the abilities a provider has, sorted so a client sees a
// stable list rather than Go's map order. Only true entries are named: a
// capability the driver reports as false is one it cannot do, which is the same
// answer as not naming it, and listing both states would invite a client to read
// presence rather than value.
func capabilityNames(caps ports.ChatCapabilities) []string {
	if len(caps) == 0 {
		return nil
	}
	names := make([]string, 0, len(caps))
	for name, has := range caps {
		if has {
			names = append(names, string(name))
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	return names
}
