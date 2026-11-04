package controllers

// AutomationIDParam identifies a recurring definition.
type AutomationIDParam struct {
	AutomationID string `path:"automationId" description:"Automation identifier."`
}

// ListAutomationsQuery describes filters and pagination for automation definitions.
type ListAutomationsQuery struct {
	ProjectID string `query:"projectId,omitempty"`
	Enabled   *bool  `query:"enabled,omitempty"`
	Limit     int    `query:"limit,omitempty" minimum:"1" maximum:"100"`
	Cursor    string `query:"cursor,omitempty"`
}

// ListAutomationRunsQuery describes pagination for an automation's run history.
type ListAutomationRunsQuery struct {
	Limit  int    `query:"limit,omitempty" minimum:"1" maximum:"100"`
	Cursor string `query:"cursor,omitempty"`
}

// AutomationEnvelope wraps one automation response.
type AutomationEnvelope struct {
	Automation AutomationResponse `json:"automation"`
}
