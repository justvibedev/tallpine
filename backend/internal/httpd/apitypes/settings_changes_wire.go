package apitypes

import (
	"time"
)

// SettingsChange is one changed setting key.
type SettingsChange struct {
	ID        int64     `json:"id"`
	ChangedAt time.Time `json:"changedAt"`
	Actor     string    `json:"actor" description:"\"user\", or \"<role>:<session id>\" for an agent token."`
	ProjectID string    `json:"projectId,omitempty" description:"Empty for an app-wide setting."`
	Key       string    `json:"key" description:"Settings registry key, e.g. project.agentRules."`
	OldValue  string    `json:"oldValue" description:"Previous value as JSON text; sensitive settings hold a redaction marker."`
	NewValue  string    `json:"newValue" description:"New value as JSON text; sensitive settings hold a redaction marker."`
}
