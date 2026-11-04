package controllers

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// EndpointsResponse is the body of GET /api/v1/endpoints. The phone re-reads
// it after every successful connect, so a rotated tunnel hostname or a changed
// LAN address is picked up without re-pairing.
type EndpointsResponse struct {
	Endpoints []mobilebridge.Endpoint `json:"endpoints"`
}

// IdentityResponse is the body of the unauthenticated GET /api/v1/identity
// probe. It is deliberately minimal: the route is reachable without the
// connection password, so it must carry nothing but an opaque host id and the
// mobile contract version.
type IdentityResponse struct {
	HostID     string `json:"hostId"`
	APIVersion int    `json:"apiVersion"`
}

// LinkPreviewQuery selects the external page to unfurl.
type LinkPreviewQuery struct {
	URL string `query:"url" description:"Absolute http(s) URL of the page to preview."`
}

// LinkPreviewResponse is the body of GET /api/v1/link-preview (200). Only URL
// is guaranteed; every other field is omitted when the page does not provide
// it, so the renderer renders whatever subset arrived.
type LinkPreviewResponse struct {
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	ImageURL    string `json:"imageUrl,omitempty"`
	SiteName    string `json:"siteName,omitempty"`
	FaviconURL  string `json:"faviconUrl,omitempty"`
}

// SetKeepAwakeRequest is the body of POST /api/v1/mobile/keep-awake.
type SetKeepAwakeRequest struct {
	Enabled   bool  `json:"enabled"`
	LidClosed *bool `json:"lidClosed,omitempty"`
}

// SetMobileFeatureRequest is the body of POST /api/v1/mobile/feature.
type SetMobileFeatureRequest struct {
	Enabled bool `json:"enabled"`
}

// SetMobilePublicBaseURLRequest is the body of POST /api/v1/mobile/public-url.
// An empty url clears the setting.
type SetMobilePublicBaseURLRequest struct {
	URL string `json:"url"`
}

// SetMobileQuickTunnelRequest is the body of POST /api/v1/mobile/quick-tunnel.
type SetMobileQuickTunnelRequest struct {
	Enabled bool `json:"enabled"`
}

// SetBrowserRemoteAccessRequest is the body of
// POST /api/v1/mobile/browser-control.
type SetBrowserRemoteAccessRequest struct {
	Enabled bool `json:"enabled"`
}

// SetRequireDevicePairingRequest is the body of
// POST /api/v1/mobile/device-pairing.
type SetRequireDevicePairingRequest struct {
	Enabled bool `json:"enabled"`
}

// SetSecurePairingRequest is the body of POST /api/v1/mobile/secure-pairing.
type SetSecurePairingRequest struct {
	Enabled bool `json:"enabled"`
}

// PushPairingIDParam is the {id} path parameter for the unpair route. It accepts
// either the phone's install ID or, from builds that predate install IDs, its
// push token.
type PushPairingIDParam struct {
	ID string `path:"id" description:"The phone's install id, or its push token for older builds."`
}

// PushDeviceTokenParam is the {token} path parameter for push-device routes.
type PushDeviceTokenParam struct {
	Token string `path:"token" description:"Expo push token (URL-encoded) identifying the device."`
}

// RegisterPushDeviceRequest is the body of POST /api/v1/push/devices. The phone
// sends its Expo push token plus a bit of descriptive metadata; the daemon keys
// the registry on the install ID (the token is an attribute and is now optional)
// and re-registering is an idempotent upsert.
type RegisterPushDeviceRequest struct {
	// Optional so the published contract matches what the daemon actually
	// accepts: app builds predating install IDs send none, and the handler
	// synthesizes a legacy one rather than rejecting them. Marking it required
	// would generate clients unable to express a request the server handles.
	InstallID string `json:"installId,omitempty" description:"Stable per-install device id, keying the registry so a rotated push token updates the same row. Optional: older app builds omit it and the daemon synthesizes one."`
	// Optional: a row represents a paired phone, not a push registration. Omitted
	// (or empty) when the phone is only announcing its identity — permission not
	// yet granted, or a build that can't mint a token. When present it must still
	// be a well-formed Expo push token.
	Token      string `json:"token,omitempty" description:"Expo push token, e.g. ExponentPushToken[...]. Optional: omitted when the phone has no push token yet."`
	Platform   string `json:"platform,omitempty" enum:"ios,android" description:"Device platform."`
	DeviceName string `json:"deviceName,omitempty" description:"Human-friendly device label."`
	HostName   string `json:"hostName,omitempty" description:"This phone's label for the host; used in OS push notification titles."`
}

// PushDeviceResponse is the stored view of a registered push device.
type PushDeviceResponse struct {
	Token      string    `json:"token,omitempty"`
	Platform   string    `json:"platform,omitempty"`
	DeviceName string    `json:"deviceName,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

// PushDeviceEnvelope is the { device } response body for a registered push device.
type PushDeviceEnvelope struct {
	Device PushDeviceResponse `json:"device"`
}

// UnregisterPushDeviceResponse is the body of DELETE /api/v1/push/devices/{token} (200).
type UnregisterPushDeviceResponse struct {
	Token   string `json:"token"`
	Deleted bool   `json:"deleted"`
}

/* ---- chat conversations ------------------------------------------------ */

// MobileDeviceResponse is one row of the desktop's mobile-device roster: the
// stored registration plus whether that phone is running the app right now.
type MobileDeviceResponse struct {
	InstallID            string    `json:"installId"`
	Token                string    `json:"token,omitempty"`
	Platform             string    `json:"platform,omitempty" enum:"ios,android"`
	DeviceName           string    `json:"deviceName,omitempty"`
	Live                 bool      `json:"live" description:"True when the phone's app is open and polling."`
	NotificationsEnabled bool      `json:"notificationsEnabled" description:"True when this device has a push token registered."`
	CreatedAt            time.Time `json:"createdAt"`
	LastSeenAt           time.Time `json:"lastSeenAt"`
}

// MobileDevicesResponse is the { devices } envelope for the roster.
type MobileDevicesResponse struct {
	Devices []MobileDeviceResponse `json:"devices"`
}
