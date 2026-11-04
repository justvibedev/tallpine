package domain

import "time"

// SettingsChange is one changed setting key in the settings change log (B202).
type SettingsChange struct {
	ID        int64
	ChangedAt time.Time
	// Actor is "user" or "<role>:<session id>" for an agent token.
	Actor string
	// ProjectID is empty for an app-wide setting.
	ProjectID ProjectID
	// Key is the settings registry key, e.g. "project.agentRules".
	Key string
	// OldValue and NewValue are JSON; a sensitive setting holds a redaction marker.
	OldValue string
	NewValue string
}
