package controllers

import (
	"tallpine.eu/tallpine/backend/internal/domain"
)

// ListUsageSessionsQuery is the query string accepted by GET
// /api/v1/usage/sessions.
type ListUsageSessionsQuery struct {
	ProjectID domain.ProjectID `query:"projectId,omitempty" description:"Optional project id filter for dashboard cards."`
}

// EstimatedCostResponse is a nano-USD estimate reused at every usage summary
// scope. Coverage stays an API fact for aggregation, catalog backfills, and
// contextual disclosure; it is never a pricing label or a mathematical
// qualifier on the presented value.
type EstimatedCostResponse struct {
	TotalNanos          int64  `json:"totalNanos" minimum:"0" format:"int64"`
	InputNanos          *int64 `json:"inputNanos" minimum:"0" format:"int64" description:"Every non-cache-read input charge, cache writes included."`
	CachedInputNanos    *int64 `json:"cachedInputNanos" minimum:"0" format:"int64"`
	OutputNanos         *int64 `json:"outputNanos" minimum:"0" format:"int64"`
	Coverage            string `json:"coverage" enum:"complete,partial"`
	ProviderAttribution string `json:"providerAttribution" enum:"observed,inferred,mixed" description:"Whether contributing billing providers were detected, inferred from model ownership, or both."`
}

// CompactSessionUsageResponse is one session card's usage summary.
type CompactSessionUsageResponse struct {
	SessionID       domain.SessionID       `json:"sessionId"`
	ProcessedTokens *int64                 `json:"processedTokens" minimum:"0" description:"Canonical input plus output. Null when either component is unknown."`
	TotalTokens     int64                  `json:"totalTokens" minimum:"0" description:"Deprecated compatibility alias for processedTokens."`
	Incomplete      bool                   `json:"incomplete"`
	EstimatedCost   *EstimatedCostResponse `json:"estimatedCost"`
}

// ListCompactSessionUsageResponse is the batch dashboard usage response.
type ListCompactSessionUsageResponse struct {
	Sessions []CompactSessionUsageResponse `json:"sessions"`
}

// ListSessionMemoryResponse is the batch memory reading for the board.
type ListSessionMemoryResponse struct {
	Sessions []SessionMemoryResponse `json:"sessions"`
	// System is host RAM for the panel's total bar. Absent where the
	// platform can't be read.
	System *SystemMemoryResponse `json:"system,omitempty"`
	// App is the resident memory of everything Tallpine runs (daemon, desktop
	// shell, every live session), for the topbar pressure indicator.
	App *AppMemoryResponse `json:"app,omitempty"`
}

// AppMemoryResponse is everything Tallpine runs at sample time, with Tallpine's own
// daemon and shell broken out so the panel can pin them as their own row.
type AppMemoryResponse struct {
	RSSBytes     uint64  `json:"rssBytes" minimum:"0"`
	ProcessCount int     `json:"processCount" minimum:"0"`
	CPUPercent   float64 `json:"cpuPercent" minimum:"0"`
	CPUMeasured  bool    `json:"cpuMeasured" description:"False when there was no earlier sample to measure against: cpuPercent is unknown, not zero."`
	// Own is the daemon and desktop shell alone, without any session.
	Own *SessionMemoryResponse `json:"own,omitempty"`
	// Reviewers is memory held by live reviewer panes, named separately from
	// Own because a reviewer has no session row: its identity is the review
	// it belongs to, and it can outlive the worker that spawned it.
	Reviewers []ReviewerMemoryResponse `json:"reviewers,omitempty"`
}

// ReviewerMemoryResponse is one live reviewer pane's process-tree reading,
// attributed back to the review record that owns it (never a session).
type ReviewerMemoryResponse struct {
	ReviewID  string                `json:"reviewId"`
	SessionID domain.SessionID      `json:"sessionId" description:"The worker session this reviewer reviews, not the reviewer's own identity."`
	Harness   string                `json:"harness"`
	Memory    SessionMemoryResponse `json:"memory"`
}

// SystemMemoryResponse is the host's headroom at sample time. The pressure
// light reads this, never Tallpine's share: a machine about to swap is red whoever
// holds the memory.
type SystemMemoryResponse struct {
	TotalBytes     uint64 `json:"totalBytes" minimum:"0"`
	AvailableBytes uint64 `json:"availableBytes" minimum:"0" description:"What the kernel would hand out without swapping (MemAvailable)."`
	SwapTotalBytes uint64 `json:"swapTotalBytes" minimum:"0"`
	SwapUsedBytes  uint64 `json:"swapUsedBytes" minimum:"0"`
	// SwapBytesPerSec is how fast pages moved to or from swap since the
	// previous sample. Sustained non-zero is the frozen-cursor signal.
	SwapBytesPerSec float64 `json:"swapBytesPerSec" minimum:"0"`
	CPUCount        int     `json:"cpuCount" minimum:"0"`
	// Load1 is the one-minute load average; over cpuCount means work is
	// queueing. -1 on a platform with no such concept (Windows); never
	// otherwise negative.
	Load1 float64 `json:"load1"`
	// CPUPercent is how busy the whole host was since the previous sample,
	// 0..100 across all cores; zero on the first sample.
	CPUPercent float64 `json:"cpuPercent" minimum:"0"`
	// CPUMeasured is false when there was no earlier sample to measure
	// against: cpuPercent is unknown, not zero.
	CPUMeasured bool `json:"cpuMeasured"`
	// PressureRaw is the kernel's memory-pressure figure: PSI "some avg10"
	// (percent of the last ten seconds a task stalled on memory) on Linux,
	// macOS's own kernel pressure level (1/2/4) on macOS, or 100 minus the
	// available percent where neither is available. Clients derive
	// fine / tight-soon / tight from it.
	PressureRaw float64 `json:"pressureRaw" minimum:"0"`
	// PressureSource says which reading produced pressureRaw.
	PressureSource string `json:"pressureSource" enum:"psi,available_pct,memorystatus"`
}

// MemoryPressureResponse is the machine's memory-pressure verdict alone, the
// same two fields SystemMemoryResponse carries, read without sampling any
// process so the board can poll it often.
type MemoryPressureResponse struct {
	PressureRaw    float64 `json:"pressureRaw" minimum:"0"`
	PressureSource string  `json:"pressureSource" enum:"psi,available_pct,memorystatus"`
}

// UsageTotalsResponse is the canonical telemetry aggregate for one scope.
//
// Provider-specific counters are no longer projected here: they live verbatim
// in each event's bounded provider usage object, where a field the provider
// adds later survives without a schema change on this boundary.
type UsageTotalsResponse struct {
	InputTokens         *int64                 `json:"inputTokens" minimum:"0" description:"Total input, including cached and uncached input."`
	CachedInputTokens   *int64                 `json:"cachedInputTokens" minimum:"0" description:"Input read from an existing provider cache. Cache hit percentage uses cachedInputTokens divided by inclusive inputTokens."`
	UncachedInputTokens *int64                 `json:"uncachedInputTokens" minimum:"0" description:"Input not read from an existing provider cache. Includes cache writes."`
	OutputTokens        *int64                 `json:"outputTokens" minimum:"0" description:"Total output, including provider-specific subsets such as reasoning output."`
	ProcessedTokens     *int64                 `json:"processedTokens" minimum:"0" description:"Canonical input plus output. Null when either component is unknown."`
	CacheReadTokens     *int64                 `json:"cacheReadTokens" minimum:"0" description:"Deprecated compatibility alias for cachedInputTokens."`
	EstimatedCost       *EstimatedCostResponse `json:"estimatedCost"`
}

// UsageModelResponse is telemetry grouped by model. The billing provider is a
// pricing input rather than a product distinction: each event was costed
// against its own provider's rates before reaching this aggregate, so one model
// stays one row even when more than one provider served it.
type UsageModelResponse struct {
	ModelID string              `json:"modelId"`
	Totals  UsageTotalsResponse `json:"totals"`
}

// UsageHarnessResponse groups model telemetry under one Tallpine harness.
type UsageHarnessResponse struct {
	Harness string               `json:"harness"`
	Totals  UsageTotalsResponse  `json:"totals"`
	Models  []UsageModelResponse `json:"models"`
}

// SessionUsageResponse is detailed telemetry for the session inspector.
type SessionUsageResponse struct {
	SessionID  domain.SessionID       `json:"sessionId"`
	Incomplete bool                   `json:"incomplete"`
	Totals     UsageTotalsResponse    `json:"totals"`
	Harnesses  []UsageHarnessResponse `json:"harnesses"`
}
