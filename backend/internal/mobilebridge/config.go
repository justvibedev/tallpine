// Package mobilebridge owns the durable state and helpers for the Connect
// Mobile LAN listener: the ~/.tallpine/mobile/config.json store and the rotating
// connection password. It has no httpd/daemon dependencies.
package mobilebridge

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DefaultPort is the LAN listener's default port for the Connect Mobile
// bridge. Distinct from config.DefaultPort (the loopback API port) since the
// two listeners can run concurrently.
const DefaultPort = 3011

// State is the persisted Connect Mobile bridge config in ~/.tallpine/mobile/config.json.
// Only hashes of credentials are persisted; the pairing code lives in memory
// (see BridgeService) and is shown once.
type State struct {
	// FeatureEnabled is the master switch for the whole mobile surface (Settings
	// > Mobile). It defaults to false: until the user turns it on there is no
	// mobile listener, no pairing/push routes, no push relay and no tunnel.
	FeatureEnabled bool `json:"featureEnabled"`
	// PublicBaseURL is an optional user-supplied reachable base URL (a Tailscale
	// MagicDNS/100.x address or the user's own reverse proxy) that pairing codes
	// advertise instead of any hosted directory. Validated by ParsePublicBaseURL.
	PublicBaseURL string `json:"publicBaseUrl,omitempty"`
	// QuickTunnel is the separate, explicit opt-in for a Cloudflare quick tunnel
	// (trycloudflare.com). Cloudflare terminates TLS for it, so it is never on
	// unless the user chooses it.
	QuickTunnel bool `json:"quickTunnel,omitempty"`
	Enabled     bool `json:"enabled"`
	// Password is read-only legacy input: files written by older daemons held
	// the plaintext connection password here. Load migrates it into
	// LegacyPasswordHash and rewrites the file without it; nothing sets it now.
	Password string `json:"password,omitempty"`
	// LegacyPasswordHash and LegacyUntil keep phones paired with the old
	// password bearer working until they upgrade to a device token (see
	// LegacyGrace). Only the hash is stored.
	LegacyPasswordHash string    `json:"legacyPasswordHash,omitempty"`
	LegacyUntil        time.Time `json:"legacyUntil,omitzero"`
	// LegacyRevoked marks a legacy password the user retired (Regenerate). Unlike
	// a window that merely ended, it is never accepted again, even if per-device
	// pairing is later turned off.
	LegacyRevoked bool `json:"legacyRevoked,omitempty"`
	// RequireDevicePairing makes LegacyUntil binding: after it, the old password
	// is refused with LEGACY_PAIRING_EXPIRED. On by default (a fresh state, or a
	// file without the key); Save always writes the key so an explicit off the
	// user chose in Settings survives Load.
	RequireDevicePairing bool `json:"requireDevicePairing"`
	// The former requireEncryptedTransport key is ignored on load: the mobile
	// listener never serves plain HTTP from the network, so there is no setting.
	LastPort int `json:"lastPort"`
	// The last rotated credential stays recognizable as stale after restart.
	RetiredPasswordHash string `json:"retiredPasswordHash,omitempty"`
	// NoPublicTunnel is retained only so old config files still parse. The
	// tunnel now runs solely when QuickTunnel is set; this field is ignored.
	NoPublicTunnel bool `json:"noPublicTunnel"`
	// LoopbackOnly keeps the authenticated listener reachable only by a local
	// connector. Old Connect Mobile configs remain LAN-bound by default.
	LoopbackOnly bool `json:"loopbackOnly,omitempty"`
	// SecurePairing is the opt-in TLS-over-Tailscale mode. Persisted so a daemon
	// restart (backend/internal/daemon/mobile_restore.go, via
	// BridgeService.RestoreOnBoot) knows to re-apply the `tailscale serve` proxy
	// — pointed at whatever port the restarted LAN listener actually bound, not
	// this struct's LastPort, since Start can fall back to an ephemeral port.
	SecurePairing bool `json:"securePairing"`
	// ServeCleanupPending survives a failed Tailscale Serve teardown so a later
	// tunnel-only setup cannot inherit a Tallpine-owned tailnet route.
	ServeCleanupPending bool `json:"serveCleanupPending,omitempty"`
	// KeepAwake is the user's choice to keep this Mac from idle-sleeping while
	// the bridge is on. Kept separate from Enabled so turning the bridge off and
	// on again does not forget it.
	KeepAwake    bool `json:"keepAwake"`
	KeepAwakeLid bool `json:"keepAwakeLid,omitempty"`
	// BrowserRemoteAccess is an explicit, additional opt-in for streaming and
	// controlling the desktop-owned browser from a paired phone. Existing
	// installations deserialize the omitted field as false.
	BrowserRemoteAccess bool `json:"browserRemoteAccess"`
}

// Path returns the Connect Mobile config file location under the data dir
// (~/.tallpine/mobile/config.json).
func Path(dataDir string) string { return filepath.Join(dataDir, "mobile", "config.json") }

// Load reads the Connect Mobile config from path. A missing file is not an
// error: it yields a disabled State with device pairing required.
func Load(path string) (State, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{RequireDevicePairing: true}, nil
	}
	if err != nil {
		return State{RequireDevicePairing: true}, fmt.Errorf("read mobile config: %w", err)
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{RequireDevicePairing: true}, fmt.Errorf("parse mobile config: %w", err)
	}
	migrated := false
	var probe struct {
		Require *bool `json:"requireDevicePairing"`
	}
	if json.Unmarshal(b, &probe) == nil && probe.Require == nil {
		s.RequireDevicePairing, migrated = true, true
	}
	if migrateLegacyPassword(&s, time.Now()) {
		migrated = true
	}
	if sealLegacyState(path, &s) {
		migrated = true
	}
	if migrated {
		// Best effort: a failed rewrite leaves the old file, and the next Load
		// migrates again. The plaintext is never used after this point.
		_ = Save(path, s)
	}
	_ = os.Chmod(path, 0o600)
	return s, nil
}

// Save atomically writes s to path (0600) via a temp file + rename, creating the
// parent dir if needed.
func Save(path string, s State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir mobile dir: %w", err)
	}
	//nolint:gosec // G117: Password is read-only legacy input, always empty on save (see State.Password).
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

const pwAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// GeneratePassword returns a fresh 8-character alphanumeric connection password
// drawn uniformly from a cryptographically secure source. Bytes at or above
// the largest multiple of the alphabet size are redrawn, so no character is
// likelier than another.
func GeneratePassword() (string, error) {
	const length = 8
	limit := byte(256 - 256%len(pwAlphabet))
	out := make([]byte, 0, length)
	buf := make([]byte, 2*length)
	for len(out) < length {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if b < limit && len(out) < length {
				out = append(out, pwAlphabet[int(b)%len(pwAlphabet)])
			}
		}
	}
	return string(out), nil
}

// HashPassword returns the hex-encoded SHA-256 of pw, the in-memory auth hash the
// LAN listener validates against (the plaintext is never used for comparison).
func HashPassword(pw string) string {
	sum := sha256.Sum256([]byte(pw))
	return hex.EncodeToString(sum[:])
}

// PasswordMatches reports whether pw hashes to hash, using a constant-time
// comparison to avoid leaking the hash via timing.
func PasswordMatches(hash, pw string) bool {
	return subtle.ConstantTimeCompare([]byte(hash), []byte(HashPassword(pw))) == 1
}
