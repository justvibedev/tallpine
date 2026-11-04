package httpd

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// lanPairExchangePath is the one mobile route reachable without a device
// token. A phone trades the single-use pairing code from the desktop QR (or,
// for a phone paired before device tokens, its legacy password as the bearer)
// for a long random per-device token.
const lanPairExchangePath = "/pair/exchange"

type pairExchangeRequest struct {
	Code       string `json:"code"`
	InstallID  string `json:"installId"`
	DeviceName string `json:"deviceName"`
}

type pairExchangeResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// pairing serializes the check-and-consume of the pairing code so two
// concurrent exchanges cannot both redeem it.
type pairing struct {
	mu     sync.Mutex
	onPair func()
}

// SetPairedHook registers a callback run (asynchronously) after a pairing code
// was redeemed, so the desktop can mint a fresh one.
func (m *LANManager) SetPairedHook(fn func()) {
	m.pair.mu.Lock()
	m.pair.onPair = fn
	m.pair.mu.Unlock()
}

// redeemCode consumes the pairing code if code matches it.
func (m *LANManager) redeemCode(code string) (hook func(), ok bool) {
	m.pair.mu.Lock()
	defer m.pair.mu.Unlock()
	h := m.state.currentHash()
	if h == "" || code == "" || !m.state.pairingCodeLive(m.state.clock()) || !mobilebridge.PasswordMatches(h, code) {
		return nil, false
	}
	m.state.setHash("")
	return m.pair.onPair, true
}

func (m *LANManager) servePairExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		envelope.WriteAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "METHOD_NOT_ALLOWED", "use POST", nil)
		return
	}
	devices := m.state.devices.Load()
	if devices == nil {
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "PAIRING_UNAVAILABLE", "pairing is not available", nil)
		return
	}
	src := clientSource(r)
	if m.lock.blocked(src) {
		envelope.WriteAPIError(w, r, http.StatusTooManyRequests, "too_many_requests", "LOCKED_OUT", "too many failed attempts; try again shortly", nil)
		return
	}
	var body pairExchangeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil && r.ContentLength != 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "BAD_BODY", "invalid request body", nil)
		return
	}
	// Accepted risk: the install id is client-supplied and travels in cleartext
	// (X-AO-Install-Id), so a holder of a valid code, or of the legacy password
	// during its grace window, can name another phone's install id and make
	// Issue replace (revoke) that phone's token. The impact is a denial of
	// service on that one pairing, not a takeover: the attacker gains only their
	// own new token. Binding the legacy path to a prior install is not possible
	// because the legacy password carries no install association.
	//
	// A token without an install id could not be revoked from the device list.
	installID := strings.TrimSpace(body.InstallID)
	if installID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INSTALL_ID_REQUIRED", "installId is required to pair", nil)
		return
	}
	if len(installID) > 128 {
		installID = installID[:128]
	}
	var onPair func()
	redeemed := false
	legacy, legacyExpired := m.state.legacyState(bearerToken(r), m.state.clock())
	if legacyExpired {
		// A stale credential is not a guess (same as authMiddleware): clients
		// retry the upgrade on every connect, and that must not feed the lockout.
		envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "LEGACY_PAIRING_EXPIRED",
			"this pairing is out of date; scan the pairing code on the desktop again", nil)
		return
	}
	if legacy {
		redeemed = true // an upgrade in place: the legacy password is already the credential
	} else if hook, ok := m.redeemCode(strings.TrimSpace(body.Code)); ok {
		redeemed, onPair = true, hook
	}
	if !redeemed {
		// A code that was just used (or has been replaced) is a retrying phone,
		// not a guess: it must not cost a lockout strike, and it gets its own
		// code so the phone does not call it a rotated password.
		if code := strings.TrimSpace(body.Code); code != "" && m.state.pairingCodeMatches(code) {
			envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "PAIRING_CODE_USED",
				"this pairing code was already used or has expired; show a new code on the desktop", nil)
			return
		}
		m.lock.fail(src)
		envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "BAD_PAIRING_CODE",
			"the pairing code is wrong or already used; show a new code on the desktop", nil)
		return
	}
	name := strings.TrimSpace(body.DeviceName)
	if len(name) > 64 {
		name = name[:64]
	}
	token, replaced, err := devices.Issue(installID, name)
	if err != nil {
		m.log.Error("issue device token", "err", err)
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "PAIRING_FAILED", "could not pair this device", nil)
		return
	}
	// This exchange may ride the keep-alive connection the phone's polls
	// authenticated with the token being replaced. Closing that connection here
	// would drop this response: the phone sees "connection lost", retries, and
	// the code it already spent answers 401. The request carries no credential
	// of its own, so detach the connection from the old token first; its next
	// request authenticates with the new one.
	forgetConnCredential(r)
	m.CloseTokenConnections(replaced...)
	// No lock.reset(src) here: this route is unauthenticated, and a reset would
	// also mark the source trusted past a spent global budget. Behind Tailscale
	// Serve that source is shared by every peer.
	if onPair != nil {
		go onPair()
	}
	w.Header().Set("Cache-Control", "no-store")
	envelope.WriteJSON(w, http.StatusOK, pairExchangeResponse{Token: token, ExpiresAt: time.Now().Add(mobilebridge.DeviceTokenTTL)})
}

// forgetConnCredential clears the token recorded on the request's LAN
// connection, so revoking that token no longer selects the connection.
func forgetConnCredential(r *http.Request) {
	conn, _ := r.Context().Value(lanConnContextKey{}).(*lanConn)
	if conn == nil {
		return
	}
	conn.owner.mu.Lock()
	conn.tokenHash = ""
	conn.owner.mu.Unlock()
}
