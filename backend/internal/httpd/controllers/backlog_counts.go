package controllers

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// inboxCounts answers the sidebar's one number: inbox items waiting for triage,
// per project. An orchestrator token sees only its own project; the human app
// sees every non-archived project.
func (c *BacklogController) inboxCounts(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/backlog/inbox-counts")
		return
	}
	counts, err := c.Svc.InboxCounts(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	agent, scoped := scopedAgent(r)
	out := make(map[string]int, len(counts))
	for id, n := range counts {
		if scoped && !agent.MayActOn(id) {
			continue
		}
		out[string(id)] = n
	}
	envelope.WriteJSON(w, http.StatusOK, BacklogInboxCountsResponse{Counts: out})
}
