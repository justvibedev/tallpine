package apitypes

// ModelUsagePayload is one model's row of ModelUsageResponse.
type ModelUsagePayload struct {
	Model               string  `json:"model"`
	InputTokens         int64   `json:"inputTokens"`
	CacheCreationTokens int64   `json:"cacheCreationTokens"`
	CacheReadTokens     int64   `json:"cacheReadTokens"`
	OutputTokens        int64   `json:"outputTokens"`
	Credits             float64 `json:"credits"`
	// ContextPercent is the context window fill (0-100) when the harness reported it.
	ContextPercent *float64 `json:"contextPercent,omitempty"`
	Turns          int64    `json:"turns"`
}
