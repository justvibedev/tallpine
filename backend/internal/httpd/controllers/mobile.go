package controllers

import (
	"encoding/json"
	"errors"
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/mobilebridge"
	bridgesvc "tallpine.eu/tallpine/backend/internal/mobilebridge/service"
)

type mobileBridge interface {
	Status() MobileStatusResponse
	Enable() (MobileStatusResponse, error)
	EnableLANOnly() (MobileStatusResponse, error)
	EnableTunnelOnly() (MobileStatusResponse, error)
	Disable() error
	Regenerate() (MobileStatusResponse, error)
	StartRemoteAccess() (MobileStatusResponse, error)
	SetSecurePairing(on bool) (MobileStatusResponse, error)
	SetKeepAwake(on bool) (MobileStatusResponse, error)
	SetBrowserRemoteAccess(on bool) (MobileStatusResponse, error)
	SetRequireDevicePairing(on bool) (MobileStatusResponse, error)
	SetKeepAwakeLid(on, lidClosed bool) (MobileStatusResponse, error)
	SetFeatureEnabled(on bool) (MobileStatusResponse, error)
	SetPublicBaseURL(raw string) (MobileStatusResponse, error)
	SetQuickTunnel(on bool) (MobileStatusResponse, error)
}

// MobileController exposes the Connect Mobile bridge control endpoints
// (status/enable/disable/regenerate) over the loopback API, delegating to a
// mobileBridge and stamping the unencrypted-LAN warning onto every response.
type MobileController struct{ Bridge mobileBridge }

// withWarning stamps the constant unencrypted-LAN warning onto any bridge
// response. The warning is not bridge-specific state — it's always present —
// so the controller guarantees it here rather than trusting every mobileBridge
// implementation (including test fakes) to set it.
func withWarning(res MobileStatusResponse) MobileStatusResponse {
	res.Warning = bridgesvc.UnencryptedWarning
	return res
}

// Status returns the current bridge status.
func (c *MobileController) Status(w http.ResponseWriter, r *http.Request) {
	envelope.WriteJSON(w, http.StatusOK, withWarning(c.Bridge.Status()))
}

// StartRemoteAccess re-checks for a connector and starts it, leaving the
// connection password alone so an already-paired phone keeps working.
func (c *MobileController) StartRemoteAccess(w http.ResponseWriter, r *http.Request) {
	res, err := c.Bridge.StartRemoteAccess()
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_REMOTE_ACCESS", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

// Enable turns the bridge on and returns the resulting status (with password).
func (c *MobileController) Enable(w http.ResponseWriter, r *http.Request) {
	res, err := c.Bridge.Enable()
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_ENABLE", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

// EnableLANOnly turns on the authenticated LAN listener without starting the
// managed public tunnel. Repeating it preserves the current password.
func (c *MobileController) EnableLANOnly(w http.ResponseWriter, r *http.Request) {
	res, err := c.Bridge.EnableLANOnly()
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_ENABLE_LAN_ONLY", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

// EnableTunnelOnly binds the authenticated listener to loopback for a managed
// connector without publishing its port on the host's network interfaces.
func (c *MobileController) EnableTunnelOnly(w http.ResponseWriter, r *http.Request) {
	res, err := c.Bridge.EnableTunnelOnly()
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "MOBILE_ENABLE_TUNNEL_ONLY", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

// Disable turns the bridge off and returns the resulting status.
func (c *MobileController) Disable(w http.ResponseWriter, r *http.Request) {
	if err := c.Bridge.Disable(); err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_DISABLE", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(c.Bridge.Status()))
}

// Regenerate rotates the connection password and returns the resulting status.
func (c *MobileController) Regenerate(w http.ResponseWriter, r *http.Request) {
	res, err := c.Bridge.Regenerate()
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_REGEN", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

// SecurePairing turns the TLS-over-Tailscale pairing mode on or off.
func (c *MobileController) SecurePairing(w http.ResponseWriter, r *http.Request) {
	var body SetSecurePairingRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "invalid_request", "MOBILE_SECURE_PAIRING", "invalid body", nil)
		return
	}
	res, err := c.Bridge.SetSecurePairing(body.Enabled)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_SECURE_PAIRING", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

// KeepAwake turns the keep-this-Mac-awake option on or off.
func (c *MobileController) KeepAwake(w http.ResponseWriter, r *http.Request) {
	var body SetKeepAwakeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "invalid_request", "MOBILE_KEEP_AWAKE", "invalid body", nil)
		return
	}
	var res MobileStatusResponse
	var err error
	if body.LidClosed != nil {
		res, err = c.Bridge.SetKeepAwakeLid(body.Enabled, *body.LidClosed)
	} else {
		res, err = c.Bridge.SetKeepAwake(body.Enabled)
	}
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_KEEP_AWAKE", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

// BrowserRemoteAccess turns paired-device browser viewing and control on or
// off. Like the other mobile control routes, the LAN listener blocks this
// route so a phone cannot grant itself access.
func (c *MobileController) BrowserRemoteAccess(w http.ResponseWriter, r *http.Request) {
	var body SetBrowserRemoteAccessRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "invalid_request", "MOBILE_BROWSER_CONTROL", "invalid body", nil)
		return
	}
	res, err := c.Bridge.SetBrowserRemoteAccess(body.Enabled)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_BROWSER_CONTROL", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

// RequireDevicePairing turns the "per-device pairing only" setting on or off.
// Like the other mobile control routes, the LAN listener blocks it.
func (c *MobileController) RequireDevicePairing(w http.ResponseWriter, r *http.Request) {
	var body SetRequireDevicePairingRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "invalid_request", "MOBILE_DEVICE_PAIRING", "invalid body", nil)
		return
	}
	res, err := c.Bridge.SetRequireDevicePairing(body.Enabled)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_DEVICE_PAIRING", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

// Feature turns the whole mobile feature on or off. Turning it off also stops
// the listener, tunnel, serve proxy and keep-awake hold.
func (c *MobileController) Feature(w http.ResponseWriter, r *http.Request) {
	var body SetMobileFeatureRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "invalid_request", "MOBILE_FEATURE", "invalid body", nil)
		return
	}
	res, err := c.Bridge.SetFeatureEnabled(body.Enabled)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_FEATURE", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

// PublicBaseURL sets (or, with an empty url, clears) the reachable base URL
// that pairing codes advertise.
func (c *MobileController) PublicBaseURL(w http.ResponseWriter, r *http.Request) {
	var body SetMobilePublicBaseURLRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "invalid_request", "MOBILE_PUBLIC_URL", "invalid body", nil)
		return
	}
	res, err := c.Bridge.SetPublicBaseURL(body.URL)
	if err != nil {
		if errors.Is(err, mobilebridge.ErrInvalidPublicBaseURL) {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "invalid_request", "MOBILE_PUBLIC_URL", err.Error(), nil)
			return
		}
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_PUBLIC_URL", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

// QuickTunnel is the explicit Cloudflare quick-tunnel opt-in.
func (c *MobileController) QuickTunnel(w http.ResponseWriter, r *http.Request) {
	var body SetMobileQuickTunnelRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "invalid_request", "MOBILE_QUICK_TUNNEL", "invalid body", nil)
		return
	}
	res, err := c.Bridge.SetQuickTunnel(body.Enabled)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_QUICK_TUNNEL", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}
