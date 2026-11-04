package httpd

import (
	"sync/atomic"
	"time"

	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// legacyCred is the hash of the old connection password plus the end of its
// grace window. It authenticates the pre-token phone builds until then.
type legacyCred struct {
	hash  string
	until time.Time
}

// deviceAuth holds the credentials the LAN middleware accepts besides the
// loopback token: per-device tokens, the grace-window legacy password, and the
// encrypted-transport requirement.
type deviceAuth struct {
	devices atomic.Pointer[mobilebridge.DeviceTokens]
	legacy  atomic.Pointer[legacyCred]
	// legacyKey seals the stored legacy hash (see mobilebridge.SealLegacyHash).
	legacyKey atomic.Pointer[[]byte]
	// requireDevicePairing makes the legacy grace window binding. On (the
	// state default, applied at start), an expired legacy password is refused;
	// off, a matching legacy password never expires.
	requireDevicePairing atomic.Bool
	// now is the clock for the legacy grace window; nil means time.Now.
	now func() time.Time
}

func (d *deviceAuth) clock() time.Time {
	if d.now != nil {
		return d.now()
	}
	return time.Now()
}

func (d *deviceAuth) verifyDevice(tok string) (mobilebridge.DeviceToken, bool) {
	if tok == "" {
		return mobilebridge.DeviceToken{}, false
	}
	if devices := d.devices.Load(); devices != nil {
		return devices.Verify(tok)
	}
	return mobilebridge.DeviceToken{}, false
}

// legacyState classifies tok against the legacy password: valid inside the
// grace window (or always, unless per-device pairing is required), expired
// after the window when it is, otherwise neither.
func (d *deviceAuth) legacyState(tok string, now time.Time) (valid, expired bool) {
	c := d.legacy.Load()
	if c == nil || c.hash == "" || tok == "" {
		return false, false
	}
	var key []byte
	if k := d.legacyKey.Load(); k != nil {
		key = *k
	}
	if !mobilebridge.LegacyPasswordMatches(key, c.hash, tok) {
		return false, false
	}
	if now.Before(c.until) || !d.requireDevicePairing.Load() {
		return true, false
	}
	return false, true
}

// SetDeviceTokens wires the durable per-device token store. Call it once the
// store is loaded; nil removes device-token authentication.
func (m *LANManager) SetDeviceTokens(t *mobilebridge.DeviceTokens) { m.state.devices.Store(t) }

// legacyTokenHash labels connections that authenticated with the legacy
// password. The stored hash is keyed, so it cannot be derived from the bearer
// the way a device token's hash is.
const legacyTokenHash = mobilebridge.LegacyCredential

// SetLegacyKey sets the per-install key that opens a sealed legacy hash.
func (m *LANManager) SetLegacyKey(key []byte) { m.state.legacyKey.Store(&key) }

// SetLegacyPassword arms (or, with an empty hash, clears) the grace-window
// bearer for phones paired before per-device tokens. Clearing closes the
// connections that authenticated with it.
func (m *LANManager) SetLegacyPassword(hash string, until time.Time) {
	prev := m.state.legacy.Load()
	if hash == "" {
		m.state.legacy.Store(nil)
	} else {
		m.state.legacy.Store(&legacyCred{hash: hash, until: until})
	}
	if prev != nil && prev.hash != "" && prev.hash != hash {
		m.CloseTokenConnections(legacyTokenHash)
	}
}

// SetRequireDevicePairing makes the legacy grace window binding (true) or lets
// a legacy pairing keep working without expiry (false; the persisted state defaults to true).
func (m *LANManager) SetRequireDevicePairing(on bool) { m.state.requireDevicePairing.Store(on) }

// CloseTokenConnections closes live connections authenticated with the given
// token hashes (device revocation).
func (m *LANManager) CloseTokenConnections(hashes ...string) {
	m.mu.Lock()
	lns := []*lanListener{m.ln, m.tunnelLn}
	m.mu.Unlock()
	for _, ln := range lns {
		if ln == nil {
			continue
		}
		for _, h := range hashes {
			if h == "" {
				continue
			}
			if err := ln.closeConnections(h); err != nil {
				m.log.Warn("close connections after token revoke", "err", err)
			}
		}
	}
}
