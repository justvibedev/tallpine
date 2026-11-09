package apitypes

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// AOImportRequest is the body of POST /api/v1/imports/ao.
type AOImportRequest struct {
	SourceDataDir   string   `json:"sourceDataDir,omitempty" description:"Official Agent Orchestrator data dir to read. Defaults to ~/.ao/data."`
	DryRun          bool     `json:"dryRun" description:"Plan only; write nothing."`
	ProjectIDs      []string `json:"projectIds,omitempty" description:"Source project ids to import. Empty selects every active project."`
	IncludeSettings bool     `json:"includeSettings,omitempty" description:"Also apply the global settings that map one-to-one."`
}

// DevImportProjectsRequest is the body of POST /api/v1/dev/import-projects.
type DevImportProjectsRequest struct {
	SourceDataDir string `json:"sourceDataDir" minLength:"1"`
	DryRun        bool   `json:"dryRun"`
}

// MergePRRequest is the body of POST /api/v1/prs/{id}/merge.
type MergePRRequest struct {
	PRURL           string `json:"prUrl" minLength:"1"`
	ExpectedHeadSHA string `json:"expectedHeadSha" minLength:"40"`
}

// MergePRResponse is the body of POST /api/v1/prs/{id}/merge (200).
type MergePRResponse struct {
	OK             bool           `json:"ok"`
	PRNumber       int            `json:"prNumber"`
	Method         string         `json:"method"`
	State          domain.PRState `json:"state" enum:"merged"`
	StateChangedAt time.Time      `json:"stateChangedAt"`
}

// MergePreflightResponse is the body of POST /api/v1/prs/{id}/merge-preflight
// (200): every merge precondition passed and nothing was merged.
type MergePreflightResponse struct {
	OK       bool `json:"ok"`
	PRNumber int  `json:"prNumber"`
}

// ResolveCommentsResponse is the body of POST /api/v1/prs/{id}/resolve-comments (200).
type ResolveCommentsResponse struct {
	OK       bool `json:"ok"`
	Resolved int  `json:"resolved"`
}
