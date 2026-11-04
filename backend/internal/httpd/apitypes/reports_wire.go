package apitypes

// CreateReportRequest is the local caller-to-daemon report submission contract.
// SessionID attributes the report; like the rest of Tallpine's unauthenticated
// loopback API, it is not cryptographic proof of worker authorship. Reports do
// not mutate or derive authoritative session status.
type CreateReportRequest struct {
	SessionID string                `json:"sessionId"`
	State     string                `json:"state,omitempty" enum:"checkpoint,needs_input,stuck,done"`
	Note      string                `json:"note,omitempty" maxLength:"1000"`
	Message   string                `json:"message,omitempty" maxLength:"1000"`
	Outputs   []ReportOutputRequest `json:"outputs,omitempty"`
	Defer     []string              `json:"defer,omitempty" description:"Follow-up lines to file as inbox backlog items: at most 3, each 1-200 characters."`
}

// ReportOutputRequest is one ordered structured output reference.
type ReportOutputRequest struct {
	Kind      string `json:"kind" enum:"artifact,pr_created,pr_reviewed"`
	Reference string `json:"reference" minLength:"1"`
	Label     string `json:"label,omitempty"`
}
