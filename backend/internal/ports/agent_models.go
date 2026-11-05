package ports

import (
	"context"
	"time"
)

// ModelSelectionMode tells clients how to render an agent's model control.
type ModelSelectionMode string

const (
	// ModelSelectionCatalog renders a model list reported by the selected agent.
	ModelSelectionCatalog ModelSelectionMode = "catalog"
	// ModelSelectionText renders a free-form model id input.
	ModelSelectionText ModelSelectionMode = "text"
	// ModelSelectionModeList renders an agent-owned mode list rather than model ids.
	ModelSelectionModeList ModelSelectionMode = "mode"
)

// CustomModelEntryMode tells clients how an agent handles models that are not
// present in its current catalog.
type CustomModelEntryMode string

const (
	// CustomModelEntryNone means the agent only accepts its reported choices.
	CustomModelEntryNone CustomModelEntryMode = "none"
	// CustomModelEntryDirect means Tallpine may pass a user-entered model id directly.
	CustomModelEntryDirect CustomModelEntryMode = "direct"
	// CustomModelEntryConfigured means custom models must first be configured in
	// the agent and then discovered by Tallpine as ordinary catalog entries.
	CustomModelEntryConfigured CustomModelEntryMode = "configured"
)

// AgentModelInfo is one model or mode that an adapter reports as selectable.
type AgentModelInfo struct {
	// ContextWindow and Inputs are populated only from provider catalog metadata.
	// Zero/nil means unknown, not an inferred capability.
	ContextWindow int64    `json:"contextWindow,omitempty"`
	Inputs        []string `json:"inputs,omitempty"`
	ID            string   `json:"id"`
	Label         string   `json:"label"`
	Provider      string   `json:"provider,omitempty"`
	IsDefault     bool     `json:"isDefault,omitempty"`
	// Efforts are the reasoning levels this specific model accepts, in the
	// provider's own ascending order. Empty means the model takes no effort
	// setting, which is a real answer rather than a missing one — Sonnet 4.5
	// and Haiku 4.5 accept none while the 5 family accepts five — so a picker
	// must render no effort control at all rather than an empty one.
	Efforts       []string `json:"efforts,omitempty"`
	DefaultEffort string   `json:"defaultEffort,omitempty"`
	// SupportsFast reports that the model can run on the provider's faster tier.
	SupportsFast bool `json:"supportsFast,omitempty"`
	// LastUsedAt is the latest activity of a session in scope whose current model
	// is this model. It is derived from session history on every read, never
	// stored on the cached catalog, and absent for a model no session ran.
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	// RequiresVersion is the oldest agent CLI version that can use this model.
	// It is set only when the installed CLI is older, so a picker can show the
	// model as unavailable instead of letting a launch fail.
	RequiresVersion string `json:"requiresVersion,omitempty"`
	// UnavailableReason is display text explaining why the model cannot be
	// selected right now and how to fix it.
	UnavailableReason string `json:"unavailableReason,omitempty"`
}

// AgentModelCatalog is Tallpine's normalized model-picker response.
type AgentModelCatalog struct {
	// Providers includes reported provider IDs even when they currently have no models.
	Providers []string `json:"providers,omitempty"`
	// ConfiguredProviders names providers with credential configuration observed
	// during discovery. Presence is not proof of authorization; absence is unknown.
	// Only provider IDs are exposed, never credential values or source paths.
	ConfiguredProviders []string `json:"configuredProviders,omitempty"`
	// RunnableModels names the models the agent itself lists as ones it can run
	// now, when the rest of Models come from a local catalog of every provider
	// it knows (OpenCode's), there to browse. Empty means not known.
	RunnableModels   []string             `json:"runnableModels,omitempty"`
	AgentID          string               `json:"agentId"`
	SelectionMode    ModelSelectionMode   `json:"selectionMode" enum:"catalog,text,mode"`
	Models           []AgentModelInfo     `json:"models"`
	CustomModelEntry CustomModelEntryMode `json:"customModelEntry" enum:"none,direct,configured"`
	// AllowCustom is retained for compatibility and is true only for direct entry.
	AllowCustom bool   `json:"allowCustom"`
	Source      string `json:"source"`
	// Metadata describes the non-sensitive installed adapter inputs used for
	// discovery (for example the resolved binary and adapter source kind).
	Metadata map[string]string `json:"metadata,omitempty"`
	// InputFingerprint changes whenever an upgrade, auth/config input, or
	// project scope could produce a different catalog.
	InputFingerprint string `json:"inputFingerprint,omitempty"`
	// BinaryVersion is the legacy wire name for Tallpine's non-sensitive executable
	// and configuration metadata fingerprint.
	BinaryVersion string     `json:"binaryVersion,omitempty"`
	FetchedAt     time.Time  `json:"fetchedAt"`
	ValidatedAt   time.Time  `json:"validatedAt,omitempty"`
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
	RefreshState  string     `json:"refreshState,omitempty" enum:"idle,queued,refreshing,error"`
	RefreshError  string     `json:"refreshError,omitempty"`
	RetryAt       *time.Time `json:"retryAt,omitempty"`
	// RefreshRecommended tells cache-first clients to revalidate in the
	// background while continuing to display the cached catalog.
	RefreshRecommended bool   `json:"refreshRecommended,omitempty"`
	Stale              bool   `json:"stale"`
	Warning            string `json:"warning,omitempty"`
	// WarningCode classifies Warning when it is an authentication problem, so
	// clients can offer the agent's login instead of showing provider text.
	// auth_required: the agent is signed out or its credential was rejected.
	// auth_expired: a stored login's access token expired; the agent CLI
	// renews it on its next run.
	WarningCode string `json:"warningCode,omitempty" enum:"auth_required,auth_expired"`
}

// ContextWindowFor is the catalog's context window for a model, 0 when the
// model is not listed or the provider reported none. An empty model means the
// catalog's default, else its largest window (a larger figure only delays
// compaction). A named model is never inferred.
func (c AgentModelCatalog) ContextWindowFor(model string) int64 {
	var largest int64
	for _, item := range c.Models {
		if item.ID == model || (model == "" && item.IsDefault) {
			if item.ContextWindow > 0 || model != "" {
				return item.ContextWindow
			}
		}
		largest = max(largest, item.ContextWindow)
	}
	if model == "" {
		// No default is marked: the largest window only delays compaction, never
		// triggers it early, so a percent still applies to a default-model chat.
		return largest
	}
	return 0
}

// Model catalog warning codes.
const (
	ModelCatalogWarningAuthRequired = "auth_required"
	ModelCatalogWarningAuthExpired  = "auth_expired"
)

// CachedAgentModelCatalog is the persistence record used by the model-catalog
// service. CatalogJSON contains a serialized AgentModelCatalog.
type CachedAgentModelCatalog struct {
	AgentID          string
	ProjectID        string
	BinaryVersion    string // Legacy field name for the discovery-input metadata fingerprint.
	CatalogJSON      string
	Source           string
	FetchedAt        time.Time
	MetadataJSON     string
	InputFingerprint string
	LastSuccessAt    time.Time
	RefreshState     string
	RefreshError     string
	RetryCount       int64
	RetryAt          time.Time
	Generation       int64
}

// AgentModelCatalogCache persists normalized model catalogs across daemon
// restarts. Global catalogs use the empty project scope; BinaryVersion tracks
// the installed agent/config fingerprint used for invalidation.
type AgentModelCatalogCache interface {
	GetAgentModelCatalog(ctx context.Context, agentID, projectID string) (CachedAgentModelCatalog, bool, error)
	ListAgentModelCatalogsByAgent(ctx context.Context, agentID string) ([]CachedAgentModelCatalog, error)
	UpsertAgentModelCatalog(ctx context.Context, record CachedAgentModelCatalog) error
}

// AgentModelCatalogScopeCache supports daemon-wide startup prefetch while
// keeping the smaller cache contract easy to fake at focused boundaries.
type AgentModelCatalogScopeCache interface {
	ListAgentModelCatalogs(ctx context.Context) ([]CachedAgentModelCatalog, error)
}

// AgentModelDiscoveryRequest describes one bounded, adapter-defined model
// discovery attempt. Args remain owned by the concrete discovery adapter.
type AgentModelDiscoveryRequest struct {
	AgentID    string
	Binary     string
	WorkingDir string
	Env        map[string]string
	// CredentialType names a cloud credential kind (e.g. "anthropic_api_key")
	// when discovery must reflect the models a *cloud* session's pushed
	// credential can run rather than the local machine's own auth. Adapters that
	// gate their catalog on provider presence (opencode) honor it; others ignore
	// it. Empty for ordinary local, project-scoped discovery.
	CredentialType string
}

// AgentModelDiscoverer isolates CLI execution and discovery-input
// fingerprinting from the core agent service.
type AgentModelDiscoverer interface {
	Discover(ctx context.Context, request AgentModelDiscoveryRequest) (AgentModelCatalog, error)
	// CatalogFingerprint summarizes every input a discovery run would read: the
	// resolved executable plus any configuration the adapter consults. The
	// service compares it against the cached catalog's fingerprint, so it must
	// change whenever the catalog those inputs produce would change, and it must
	// stay cheap enough to compute before deciding to skip discovery.
	CatalogFingerprint(ctx context.Context, request AgentModelDiscoveryRequest) string
	Manual(agentID string) AgentModelCatalog
}
