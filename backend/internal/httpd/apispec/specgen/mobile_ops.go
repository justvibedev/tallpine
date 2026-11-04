package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// mobileOperations declares the 5 /mobile control operations. These are
// mounted on the loopback router (mountMobile in router.go), not the REST
// /api/v1 group — only the desktop/CLI may enable, disable, or regenerate the
// phone's LAN access; the phone never toggles its own connection. Must stay
// 1:1 with the routes mountMobile registers (enforced by the parity test).
func mobileOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/mobile/status", id: "getMobileStatus", tag: "mobile",
			summary: "Check whether Connect Mobile's LAN bridge is enabled",
			resps: []respUnit{
				{http.StatusOK, controllers.MobileStatusResponse{}},
				{http.StatusForbidden, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/mobile/remote-access", id: "startMobileRemoteAccess", tag: "mobile",
			summary: "Look for a connector again and start it, without rotating the password",
			resps: []respUnit{
				{http.StatusOK, controllers.MobileStatusResponse{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/mobile/enable", id: "enableMobile", tag: "mobile",
			summary: "Enable the Connect Mobile LAN bridge and issue a fresh password",
			resps: []respUnit{
				{http.StatusOK, controllers.MobileStatusResponse{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/mobile/enable-lan-only", id: "enableMobileLANOnly", tag: "mobile",
			summary: "Enable the authenticated LAN bridge without a managed public tunnel",
			resps: []respUnit{
				{http.StatusOK, controllers.MobileStatusResponse{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/mobile/enable-tunnel-only", id: "enableMobileTunnelOnly", tag: "mobile",
			summary: "Enable the authenticated listener on loopback for a managed tunnel",
			resps: []respUnit{
				{http.StatusOK, controllers.MobileStatusResponse{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/mobile/disable", id: "disableMobile", tag: "mobile",
			summary: "Disable the Connect Mobile LAN bridge",
			resps: []respUnit{
				{http.StatusOK, controllers.MobileStatusResponse{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/mobile/regenerate", id: "regenerateMobile", tag: "mobile",
			summary: "Rotate the Connect Mobile password, dropping any connected phone",
			resps: []respUnit{
				{http.StatusOK, controllers.MobileStatusResponse{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/mobile/secure-pairing", id: "setMobileSecurePairing", tag: "mobile",
			summary: "Turn TLS-over-Tailscale secure pairing on or off",
			reqBody: controllers.SetSecurePairingRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.MobileStatusResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/mobile/keep-awake", id: "setMobileKeepAwake", tag: "mobile",
			summary: "Keep this Mac from idle-sleeping while Connect Mobile is on",
			reqBody: controllers.SetKeepAwakeRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.MobileStatusResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/mobile/device-pairing", id: "setMobileRequireDevicePairing", tag: "mobile",
			summary: "Require per-device pairing, ending legacy pairings after their grace window",
			reqBody: controllers.SetRequireDevicePairingRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.MobileStatusResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/mobile/browser-control", id: "setMobileBrowserRemoteAccess", tag: "mobile",
			summary: "Allow or deny paired-device access to the desktop browser",
			reqBody: controllers.SetBrowserRemoteAccessRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.MobileStatusResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
	}
}

// mobileDeviceOperations declares the desktop-only mobile device roster
// routes. These sit under /api/v1/mobile — like mobileOperations above — so
// they inherit the LAN listener's transport-level block; a paired phone can
// neither list nor manage the household's other devices. Must stay 1:1 with
// the routes mountMobileDevices registers (enforced by the parity test).
func mobileDeviceOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/mobile/devices", id: "listMobileDevices", tag: "mobile",
			summary: "List paired mobile devices with their live status",
			resps: []respUnit{
				{http.StatusOK, controllers.MobileDevicesResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusServiceUnavailable, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/mobile/devices/{installId}", id: "removeMobileDevice", tag: "mobile",
			summary:    "Remove a paired device from the roster",
			pathParams: []any{controllers.InstallIDParam{}},
			resps: []respUnit{
				{http.StatusNoContent, nil},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusServiceUnavailable, envelope.APIError{}},
			},
		},
	}
}
