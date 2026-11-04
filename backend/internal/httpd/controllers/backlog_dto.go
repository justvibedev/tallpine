package controllers

// BacklogProjectIDParam is the {projectId} path parameter of the backlog routes.
type BacklogProjectIDParam struct {
	ProjectID string `path:"projectId" description:"Project that owns the backlog."`
}

// BacklogRefParam is the {ref} path parameter: a ref such as B12 (or 12), or an item id.
type BacklogRefParam struct {
	Ref string `path:"ref" description:"Item ref (B12 or 12) or item id."`
}

// BacklogFieldParam is the {key} path parameter of the custom-field routes.
type BacklogFieldParam struct {
	Key string `path:"key" description:"Custom field key: [a-z0-9][a-z0-9._-]{0,39}."`
}

// BacklogItemEnvelope is the { item } body of item reads and mutations.
type BacklogItemEnvelope struct {
	Item BacklogItemResponse `json:"item"`
}

// ListBacklogResponse is the body of GET /projects/{projectId}/backlog.
type ListBacklogResponse struct {
	Items []BacklogItemResponse `json:"items"`
}

// NextBacklogResponse is the body of GET /projects/{projectId}/backlog/next.
type NextBacklogResponse struct {
	Item *BacklogItemResponse `json:"item" description:"Null when nothing is startable."`
}

// UpdateBacklogRequest is the partial body of PATCH /projects/{projectId}/backlog/{ref}.
// Omitted fields are unchanged; an empty parent detaches the item and an empty scope clears the override.
type UpdateBacklogRequest struct {
	Title      *string `json:"title,omitempty"`
	Note       *string `json:"note,omitempty"`
	DoneWhen   *string `json:"doneWhen,omitempty"`
	Scope      *string `json:"scope,omitempty"`
	Hold       *string `json:"hold,omitempty"`
	Parent     *string `json:"parent,omitempty"`
	AgentNotes *string `json:"agentNotes,omitempty"`
}

// BacklogFieldRequest is the body of PUT .../backlog/{ref}/fields/{key}.
type BacklogFieldRequest struct {
	Value string `json:"value" description:"New value; empty clears the field."`
}

// DeferOutcomeResponse reports what happened to one --defer line.
type DeferOutcomeResponse struct {
	Line   string `json:"line"`
	Status string `json:"status" description:"filed, merged, inbox_full or session_cap."`
	Ref    string `json:"ref,omitempty"`
	Reason string `json:"reason,omitempty" description:"Why the line was not filed."`
}

// BacklogListQuery holds the optional filters of GET /projects/{projectId}/backlog.
type BacklogListQuery struct {
	State  string `query:"state,omitempty" enum:"inbox,open,done,dropped,all" description:"Only items in this state; all includes closed items."`
	Parent string `query:"parent,omitempty" description:"Only children of this parent ref."`
	Held   string `query:"held,omitempty" enum:"held,unheld" description:"Only held or only unheld items."`
	Q      string `query:"q,omitempty" description:"Case-insensitive title substring."`
	Limit  string `query:"limit,omitempty" description:"Maximum items (default 100, max 500)."`
}

// BacklogNextQuery holds the optional filter of GET /projects/{projectId}/backlog/next.
type BacklogNextQuery struct {
	Parent string `query:"parent,omitempty" description:"Only children of this parent ref."`
}

// BacklogInboxCountsResponse is the body of GET /backlog/inbox-counts.
type BacklogInboxCountsResponse struct {
	Counts map[string]int `json:"counts" description:"Inbox items waiting for triage per non-archived project; projects with none are absent."`
}
