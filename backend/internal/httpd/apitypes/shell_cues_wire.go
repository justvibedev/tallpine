package apitypes

import (
	"time"
)

// CueDefinitionRequest is the complete editable definition accepted when
// creating or replacing a cue.
type CueDefinitionRequest struct {
	Name    string `json:"name" maxLength:"64" description:"Short cue name, unique within the project. Trimmed; must be non-empty and at most 64 bytes."`
	Type    string `json:"type" description:"Cue kind: command sends to a project- or session-scoped shell terminal; agent sends an authored prompt. Definition body limit: 128 KiB."`
	Command string `json:"command,omitempty" maxLength:"4096" description:"Shell command for a command cue. At most 4096 bytes; cleared when saving agent cues."`
	Prompt  string `json:"prompt,omitempty" maxLength:"16384" description:"Agent instruction for an agent cue. At most 16384 bytes; cleared when saving command cues."`
}

// CueResponse is one project-scoped reusable quick action.
type CueResponse struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Command   string    `json:"command,omitempty"`
	Prompt    string    `json:"prompt,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
