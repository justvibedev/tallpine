package controllers

import (
	"time"
)

// CreateReportResponse returns only the durable identifier needed to correlate
// a successful submission without echoing report contents.
type CreateReportResponse struct {
	ID       string                 `json:"id"`
	Deferred []DeferOutcomeResponse `json:"deferred,omitempty" description:"Outcome of each --defer line, present when the report carried any."`
	Notice   string                 `json:"notice,omitempty" description:"Where the report goes when the project has no orchestrator."`
}

// ReportOutputResponse is one ordered output reference in the read projection.
type ReportOutputResponse struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
	Label     string `json:"label,omitempty"`
}

// ReportResponse is one persisted worker claim, independent of delivery state.
type ReportResponse struct {
	ID          string                 `json:"id"`
	SessionID   string                 `json:"sessionId"`
	ProjectID   string                 `json:"projectId"`
	State       string                 `json:"state,omitempty"`
	Note        string                 `json:"note,omitempty"`
	Message     string                 `json:"message,omitempty"`
	Outputs     []ReportOutputResponse `json:"outputs,omitempty"`
	CreatedAt   time.Time              `json:"createdAt"`
	RepeatCount int64                  `json:"repeatCount"`
	// Undelivered is why Tallpine settled this report without delivering it to an
	// orchestrator (its project was archived). Absent for delivered or pending
	// reports.
	Undelivered string `json:"undelivered,omitempty"`
}

// ListReportsResponse contains a project's ordered persisted reports.
type ListReportsResponse struct {
	Reports []ReportResponse `json:"reports"`
}

// ListReportsQuery selects reports by stable project identity.
type ListReportsQuery struct {
	ProjectID string `query:"projectId" required:"true" description:"Stable project identifier."`
}
