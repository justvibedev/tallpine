package httpd

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/peeradmit"
)

// PeerNotAdmittedCode refuses a valid token presented by a process Tallpine
// did not start (B631).
const PeerNotAdmittedCode = "PEER_NOT_ADMITTED"

const peerNotAdmittedMessage = "This daemon acts only for the " + brand.ProductName +
	" desktop app, the agents it starts, and terminals opened in the app; it refused this process although its token is valid. " +
	"Use the desktop app, or run " + brand.CLIName + " from an agent session or a terminal opened in " + brand.ProductName + ". " +
	"The check can be turned off in " + brand.ProductName + " Settings."

// admitPeer gates a request that presented a valid token. It runs only after
// the token is verified and never for exempt routes. When the peer check
// enforces and the peer is not admitted it writes 403 and returns false;
// otherwise the request proceeds (report mode only records).
func admitPeer(peers *peeradmit.Checker, w http.ResponseWriter, r *http.Request, credential peeradmit.Credential) bool {
	if peers.Admit(r.Context(), peerObservation(r, credential)) {
		return true
	}
	envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", PeerNotAdmittedCode, peerNotAdmittedMessage, nil)
	return false
}

// peerObservation describes r for the peer check. No connection facts (an
// in-process caller) counts like TCP: unidentified.
func peerObservation(r *http.Request, credential peeradmit.Credential) peeradmit.Observation {
	peer, ok := daemonsock.PeerFrom(r.Context())
	if !ok {
		peer = daemonsock.Peer{Transport: daemonsock.TransportTCP}
	}
	return peeradmit.Observation{Peer: peer, Credential: credential, Method: r.Method, Path: r.URL.Path}
}

// agentCredential names the roots a verified scoped token must run under.
func agentCredential(agent requestscope.Agent) peeradmit.Credential {
	if agent.Role == requestscope.RoleReviewer {
		return peeradmit.Credential{Kind: peeradmit.CredentialReview, ReviewID: agent.ReviewID}
	}
	return peeradmit.Credential{Kind: peeradmit.CredentialSession, SessionID: string(agent.SessionID)}
}
