package controllers

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	agentsvc "tallpine.eu/tallpine/backend/internal/service/agent"
)

// ListAgentsResponse is the body of GET /api/v1/agents.
type ListAgentsResponse = agentsvc.Inventory

// RefreshAgentsResponse is the body of POST /api/v1/agents/refresh.
type RefreshAgentsResponse = agentsvc.Inventory

// ProbeAgentResponse is the body of POST /api/v1/agents/{agent}/probe.
type ProbeAgentResponse = agentsvc.ProbeResult

// AgentReadinessResponse is the normalized cached or ensured harness view.
type AgentReadinessResponse = agentsvc.Readiness

// CodexAccountsResponse is the controller-owned, redacted cached account view.
type CodexAccountsResponse struct {
	ActiveAccountID      string                            `json:"activeAccountId,omitempty"`
	AccountRevision      int64                             `json:"accountRevision"`
	Accounts             []CodexAccountResponse            `json:"accounts"`
	Capabilities         CodexAccountCapabilitiesResponse  `json:"capabilities"`
	DeviceReconciliation CodexDeviceReconciliationResponse `json:"deviceReconciliation"`
	ActiveLogin          *CodexActiveLoginResponse         `json:"activeLogin,omitempty"`
	CurrentSwitch        *CodexAccountSwitchResponse       `json:"currentSwitch,omitempty"`
}

// CodexDeviceReconciliationResponse reports whether the device credential was
// locally associated with a saved account.
type CodexDeviceReconciliationResponse struct {
	Status                string     `json:"status" enum:"not_checked,checking,verified,temporarily_unavailable,blocked"`
	ActiveAccountVerified bool       `json:"activeAccountVerified"`
	ReasonCode            string     `json:"reasonCode"`
	Retryable             bool       `json:"retryable"`
	AttemptedAt           *time.Time `json:"attemptedAt,omitempty"`
	VerifiedAt            *time.Time `json:"verifiedAt,omitempty"`
	NextRetryAt           *time.Time `json:"nextRetryAt,omitempty"`
}

// CodexAccountResponse contains UI account facts without provider or storage identity.
type CodexAccountResponse struct {
	ID             string                            `json:"id"`
	Label          string                            `json:"label"`
	Status         string                            `json:"status" enum:"valid,signed_out,broken"`
	ReasonCode     string                            `json:"reasonCode"`
	Reason         string                            `json:"reason"`
	Active         bool                              `json:"active"`
	Authentication CodexAuthenticationResponse       `json:"authentication"`
	AuthMethod     string                            `json:"authMethod" enum:"chatgpt,api_key,other,unknown"`
	AccountEmail   *string                           `json:"accountEmail,omitempty"`
	Capacity       CodexAccountCapacityResponse      `json:"capacity"`
	UsageSummary   *CodexAccountUsageSummaryResponse `json:"usageSummary,omitempty"`
	CreatedAt      time.Time                         `json:"createdAt"`
}

// CodexAuthenticationResponse is the normalized authentication observation.
type CodexAuthenticationResponse struct {
	State       string     `json:"state" enum:"authorized,unauthorized,unknown,not_applicable"`
	Freshness   string     `json:"freshness" enum:"fresh,stale,checking"`
	CheckedAt   *time.Time `json:"checkedAt"`
	AttemptedAt *time.Time `json:"attemptedAt"`
	ReasonCode  string     `json:"reasonCode"`
	Reason      string     `json:"reason"`
}

// CodexAccountCapacityResponse is the normalized capacity display projection.
type CodexAccountCapacityResponse struct {
	State             string                            `json:"state" enum:"available,near_limit,exhausted,unknown,unsupported"`
	Freshness         string                            `json:"freshness" enum:"fresh,stale,checking"`
	Plan              *string                           `json:"plan,omitempty"`
	UsedPercent       *float64                          `json:"usedPercent,omitempty" minimum:"0" maximum:"100"`
	RemainingPercent  *float64                          `json:"remainingPercent,omitempty" minimum:"0" maximum:"100"`
	ResetsAt          *time.Time                        `json:"resetsAt,omitempty"`
	ObservedAt        *time.Time                        `json:"observedAt,omitempty"`
	CheckedAt         *time.Time                        `json:"checkedAt,omitempty"`
	AttemptedAt       *time.Time                        `json:"attemptedAt,omitempty"`
	ReasonCode        string                            `json:"reasonCode"`
	Reason            string                            `json:"reason"`
	Overall           *CodexCapacityBucketResponse      `json:"overall,omitempty"`
	AdditionalBuckets []CodexCapacityBucketResponse     `json:"additionalBuckets"`
	ResetCredits      *CodexResetCreditsSummaryResponse `json:"resetCredits,omitempty"`
}

// CodexCapacityBucketResponse omits the provider limit identifier.
type CodexCapacityBucketResponse struct {
	DisplayName *string                      `json:"displayName,omitempty"`
	Primary     *CodexCapacityWindowResponse `json:"primary,omitempty"`
	Secondary   *CodexCapacityWindowResponse `json:"secondary,omitempty"`
	Reached     string                       `json:"reached" enum:"not_reached,reached,unknown"`
}

// CodexCapacityWindowResponse contains a normalized provider meter window.
type CodexCapacityWindowResponse struct {
	UsedPercent           float64    `json:"usedPercent" minimum:"0" maximum:"100"`
	WindowDurationMinutes *int64     `json:"windowDurationMinutes,omitempty"`
	ResetsAt              *time.Time `json:"resetsAt,omitempty"`
}

// CodexResetCreditsSummaryResponse contains no provider reset-credit identity.
type CodexResetCreditsSummaryResponse struct {
	AvailableCount   int64      `json:"availableCount" minimum:"0"`
	NearestExpiresAt *time.Time `json:"nearestExpiresAt,omitempty"`
}

// CodexAccountUsageSummaryResponse contains normalized aggregate usage metrics.
type CodexAccountUsageSummaryResponse struct {
	LatestDayTokens           *int64    `json:"latestDayTokens,omitempty"`
	LatestDayStartDate        *string   `json:"latestDayStartDate,omitempty"`
	LifetimeTokens            *int64    `json:"lifetimeTokens,omitempty"`
	PeakDailyTokens           *int64    `json:"peakDailyTokens,omitempty"`
	LongestRunningTurnSeconds *int64    `json:"longestRunningTurnSeconds,omitempty"`
	CurrentStreakDays         *int64    `json:"currentStreakDays,omitempty"`
	LongestStreakDays         *int64    `json:"longestStreakDays,omitempty"`
	ObservedAt                time.Time `json:"observedAt"`
}

// CodexCapabilityObservationResponse is one UI-safe capability result.
type CodexCapabilityObservationResponse struct {
	State      string `json:"state" enum:"supported,unsupported,unknown"`
	ReasonCode string `json:"reasonCode"`
	Reason     string `json:"reason"`
}

// CodexAccountCapabilitiesResponse is the renderer-consumed capability view.
type CodexAccountCapabilitiesResponse struct {
	NativeLogin        CodexCapabilityObservationResponse `json:"nativeLogin"`
	ResetCreditConsume CodexCapabilityObservationResponse `json:"resetCreditConsume"`
	GlobalSwitch       CodexCapabilityObservationResponse `json:"globalSwitch"`
}

// EnsureCodexAccountsRequest selects accounts for display reads.
type EnsureCodexAccountsRequest struct {
	AccountIDs                []string `json:"accountIds,omitempty"`
	IncludeUsage              bool     `json:"includeUsage,omitempty"`
	ForceAuthentication       bool     `json:"forceAuthentication,omitempty"`
	ForceDeviceReconciliation bool     `json:"forceDeviceReconciliation,omitempty"`
}

// ConsumeCodexAccountResetCreditRequest identifies one idempotent provider
// reset attempt. The provider selects the available reset credit.
type ConsumeCodexAccountResetCreditRequest struct {
	IdempotencyKey string `json:"idempotencyKey" minLength:"1" maxLength:"200"`
}

// OpenCodexAccountLoginTerminalResponse is the standalone terminal opened for
// one pending account's native Codex login flow.
type OpenCodexAccountLoginTerminalResponse struct {
	Operation     CodexAccountLoginResponse         `json:"operation"`
	ShellTerminal CodexAccountLoginTerminalResponse `json:"shellTerminal"`
}

// CodexAccountLoginResponse is the redacted login-operation projection.
type CodexAccountLoginResponse struct {
	OperationID string                `json:"operationId"`
	AccountID   string                `json:"accountId,omitempty"`
	Status      string                `json:"status" enum:"pending,verifying,unauthorized,retryable,completed,cancelled,failed,expired"`
	ReasonCode  string                `json:"reasonCode"`
	Reason      string                `json:"reason"`
	Account     *CodexAccountResponse `json:"account,omitempty"`
	ExpiresAt   time.Time             `json:"expiresAt"`
}

// CodexActiveLoginResponse lets a renderer remount reattach to a live login.
type CodexActiveLoginResponse struct {
	OperationID   string                            `json:"operationId"`
	AccountID     string                            `json:"accountId,omitempty"`
	Status        string                            `json:"status" enum:"pending,verifying,unauthorized,retryable,completed,cancelled,failed,expired"`
	ReasonCode    string                            `json:"reasonCode"`
	Reason        string                            `json:"reason"`
	ExpiresAt     time.Time                         `json:"expiresAt"`
	ShellTerminal CodexAccountLoginTerminalResponse `json:"shellTerminal"`
}

// CodexAccountLoginTerminalResponse contains only the mux identity and display
// fields needed by the inline Settings terminal. Its private credential-home
// working directory is deliberately excluded from the public API.
type CodexAccountLoginTerminalResponse struct {
	HandleID  string    `json:"handleId"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
}

// StartCodexAccountSwitchRequest requests an idempotent global account change.
type StartCodexAccountSwitchRequest struct {
	TargetAccountID string `json:"targetAccountId" minLength:"1"`
	// ExpectedAccountRevision is accepted temporarily for older clients and ignored.
	ExpectedAccountRevision *int64 `json:"expectedAccountRevision,omitempty" minimum:"0" deprecated:"true"`
	IdempotencyKey          string `json:"idempotencyKey" minLength:"1"`
}

// CodexAccountSwitchPhase is the retained public switch lifecycle.
type CodexAccountSwitchPhase string

// CodexAccountSwitchResponse contains only safe Tallpine identifiers and progress.
type CodexAccountSwitchResponse struct {
	ID                     string                  `json:"id"`
	SourceKind             string                  `json:"sourceKind" enum:"managed,device,none"`
	SourceAccountID        string                  `json:"sourceAccountId,omitempty"`
	TargetAccountID        string                  `json:"targetAccountId"`
	Phase                  CodexAccountSwitchPhase `json:"phase" enum:"requested,checkpointing_source,activating_target,recovery_required,completed,failed"`
	FailureCode            string                  `json:"failureCode,omitempty"`
	CredentialsCommittedAt *time.Time              `json:"credentialsCommittedAt,omitempty"`
	CreatedAt              time.Time               `json:"createdAt"`
	UpdatedAt              time.Time               `json:"updatedAt"`
	CompletedAt            *time.Time              `json:"completedAt,omitempty"`
}

// AgentReadinessSnapshot is one normalized harness readiness view.
type AgentReadinessSnapshot = domain.AgentReadinessSnapshot

// AgentInstallationObservation is the normalized binary-presence observation.
type AgentInstallationObservation = domain.AgentInstallationObservation

// AgentAuthenticationObservation is the normalized authentication observation.
type AgentAuthenticationObservation = domain.AgentAuthenticationObservation

// AgentModelsQuery reads a model catalog. Catalogs are device-wide: a project
// ID is accepted for compatibility and ignored; only a cloud credential scope
// caches separately.
type AgentModelsQuery struct {
	CatalogOnly bool   `query:"catalogOnly,omitempty" description:"Return catalog facts without reading session history for model recency. Cached catalogs are served while discovery refreshes in the background."`
	ProjectID   string `query:"projectId,omitempty" description:"Optional cloud credential scope. Any other value, including a project identifier, is ignored: model catalogs are cached per device, not per project."`
}

// AgentModelsRefreshQuery controls forced refresh versus cheap background
// revalidation for a device-wide model catalog.
type AgentModelsRefreshQuery struct {
	CatalogOnly bool   `query:"catalogOnly,omitempty" description:"Refresh the Harness Settings browsing catalog without session-history enrichment; FX includes all connections in a separate cache."`
	ProjectID   string `query:"projectId,omitempty" description:"Optional cloud credential scope. Any other value, including a project identifier, is ignored: model catalogs are cached per device, not per project."`
	Revalidate  bool   `query:"revalidate,omitempty" description:"When true, compare executable and config metadata before running discovery."`
}

// AgentModelsResponse is the normalized model picker for one agent.
type AgentModelsResponse = ports.AgentModelCatalog

// AgentModelInfo is one selectable model or agent-owned mode.
type AgentModelInfo = ports.AgentModelInfo

// AgentInfo is one supported or installed agent entry.
type AgentInfo = agentsvc.Info
