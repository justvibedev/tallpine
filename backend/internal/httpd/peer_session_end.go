package httpd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

// admitAgentPeer runs the peer check for a verified scoped token. A reviewer's
// session-end hook fires after its runtime is gone, so one refused by the check
// is dropped as a quiet 200 no-op (no grace wait, no 403, nothing applied).
// Every other request from outside the roots is refused as before.
func (s *agentScopes) admitAgentPeer(w http.ResponseWriter, r *http.Request, agent requestscope.Agent) bool {
	if !isReviewerSessionEnd(r, agent) {
		return admitPeer(s.peers, w, r, agentCredential(agent))
	}
	if !s.peers.AdmitNoGrace(r.Context(), peerObservation(r, agentCredential(agent))) {
		s.log.Debug("dropped reviewer session-end hook from outside its runtime", "reviewId", agent.ReviewID)
		envelope.WriteJSON(w, http.StatusOK, controllers.SetReviewActivityResponse{OK: true, ReviewSessionID: agent.ReviewID})
		return false
	}
	return true
}

// isReviewerSessionEnd reports whether r is a reviewer's own session-end
// activity hook. It restores the body it reads.
func isReviewerSessionEnd(r *http.Request, agent requestscope.Agent) bool {
	if agent.Role != requestscope.RoleReviewer || r.Method != http.MethodPost ||
		r.URL.Path != "/api/v1/reviews/"+agent.ReviewID+"/activity" || r.Body == nil {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(body), r.Body), r.Body}
	if err != nil {
		return false
	}
	var in struct {
		Event string `json:"event"`
	}
	return json.Unmarshal(body, &in) == nil && in.Event == "session-end"
}
