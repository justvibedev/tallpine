package apitypes

import (
	"time"
)

// AutomationResponse is the API representation of an automation definition.
type AutomationResponse struct {
	ID          string                        `json:"id"`
	ProjectID   string                        `json:"projectId"`
	DisplayName string                        `json:"displayName"`
	Prompt      string                        `json:"prompt"`
	Kind        string                        `json:"kind" enum:"worker,orchestrator"`
	Harness     string                        `json:"harness,omitempty"`
	RRule       string                        `json:"rrule"`
	Timezone    string                        `json:"timezone"`
	Enabled     bool                          `json:"enabled"`
	NextRunAt   time.Time                     `json:"nextRunAt"`
	LastRunAt   *time.Time                    `json:"lastRunAt,omitempty"`
	CreatedAt   time.Time                     `json:"createdAt"`
	UpdatedAt   time.Time                     `json:"updatedAt"`
	LatestRun   *AutomationRunSummaryResponse `json:"latestRun,omitempty"`
}

// AutomationRunResponse is the API representation of one scheduled occurrence.
type AutomationRunResponse struct {
	ID             string     `json:"id"`
	AutomationID   string     `json:"automationId"`
	ScheduledFor   time.Time  `json:"scheduledFor"`
	SessionID      string     `json:"sessionId,omitempty"`
	Status         string     `json:"status" enum:"pending,spawning,running,completed,failed"`
	AttemptCount   int64      `json:"attemptCount"`
	ClaimedAt      *time.Time `json:"claimedAt,omitempty"`
	LeaseExpiresAt *time.Time `json:"leaseExpiresAt,omitempty"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
	ErrorMessage   string     `json:"errorMessage,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

// AutomationRunSummaryResponse is the latest-run projection attached to a definition.
type AutomationRunSummaryResponse struct {
	ID           string     `json:"id"`
	Status       string     `json:"status" enum:"pending,spawning,running,completed,failed"`
	ScheduledFor time.Time  `json:"scheduledFor"`
	SessionID    string     `json:"sessionId,omitempty"`
	ErrorMessage string     `json:"errorMessage,omitempty"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
}

// CreateAutomationRequest is the body accepted when creating an automation.
type CreateAutomationRequest struct {
	ProjectID   string `json:"projectId"`
	DisplayName string `json:"displayName"`
	Prompt      string `json:"prompt"`
	Kind        string `json:"kind" enum:"worker,orchestrator"`
	Harness     string `json:"harness,omitempty"`
	RRule       string `json:"rrule,omitempty"`
	Cron        string `json:"cron,omitempty"`
	Timezone    string `json:"timezone"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

// ListAutomationRunsResponse is one page of automation run history.
type ListAutomationRunsResponse struct {
	Runs       []AutomationRunResponse `json:"runs"`
	NextCursor string                  `json:"nextCursor,omitempty"`
}

// ListAutomationsResponse is one page of automation definitions.
type ListAutomationsResponse struct {
	Automations []AutomationResponse `json:"automations"`
	NextCursor  string               `json:"nextCursor,omitempty"`
}

// UpdateAutomationRequest is the partial body accepted when updating an automation.
type UpdateAutomationRequest struct {
	DisplayName *string `json:"displayName,omitempty"`
	Prompt      *string `json:"prompt,omitempty"`
	Kind        *string `json:"kind,omitempty" enum:"worker,orchestrator"`
	Harness     *string `json:"harness,omitempty"`
	RRule       *string `json:"rrule,omitempty"`
	Cron        *string `json:"cron,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
}
