package httpd

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/peeradmit"
)

// observePeer records who presented a valid token. It runs only after the
// token is verified and never for exempt routes. It cannot change the response.
func observePeer(peers *peeradmit.Checker, r *http.Request, credential peeradmit.Credential) {
	if peers == nil {
		return
	}
	// No connection facts (an in-process caller) counts like TCP: unidentified.
	peer, ok := daemonsock.PeerFrom(r.Context())
	if !ok {
		peer = daemonsock.Peer{Transport: daemonsock.TransportTCP}
	}
	peers.Observe(peeradmit.Observation{Peer: peer, Credential: credential, Method: r.Method, Path: r.URL.Path})
}

// agentCredential names the roots a verified scoped token must run under.
func agentCredential(agent requestscope.Agent) peeradmit.Credential {
	if agent.Role == requestscope.RoleReviewer {
		return peeradmit.Credential{Kind: peeradmit.CredentialReview, ReviewID: agent.ReviewID}
	}
	return peeradmit.Credential{Kind: peeradmit.CredentialSession, SessionID: string(agent.SessionID)}
}
