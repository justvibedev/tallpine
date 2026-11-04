package mobilebridge

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	// DeviceTokenTTL is how long a device token stays valid after its last use.
	DeviceTokenTTL = 90 * 24 * time.Hour
	// deviceTokenTouchEvery bounds disk writes from the sliding expiry.
	deviceTokenTouchEvery = time.Hour
	// maxDeviceTokens caps the file; the least recently used token is evicted.
	maxDeviceTokens   = 64
	deviceTokenPrefix = "vot_"
)

// DeviceToken is the durable record of one paired phone's credential. Only the
// SHA-256 of the token is stored; the token itself is shown to the phone once.
type DeviceToken struct {
	Hash       string    `json:"hash"`
	InstallID  string    `json:"installId,omitempty"`
	DeviceName string    `json:"deviceName,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastUsedAt time.Time `json:"lastUsedAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

type deviceTokensFile struct {
	Tokens []DeviceToken `json:"tokens"`
}

// DeviceTokensPath returns ~/.tallpine/mobile/device-tokens.json under dataDir.
func DeviceTokensPath(dataDir string) string {
	return filepath.Join(dataDir, "mobile", "device-tokens.json")
}

// DeviceTokens issues and verifies per-device LAN credentials. Tokens are 256
// bits of randomness, so a hash lookup (no constant-time compare) is safe: the
// map key is a one-way hash of the secret, not the secret.
type DeviceTokens struct {
	mu     sync.Mutex
	path   string
	now    func() time.Time
	byHash map[string]DeviceToken
}

// LoadDeviceTokens reads the file at path. A missing file is an empty store.
func LoadDeviceTokens(path string) (*DeviceTokens, error) {
	d := &DeviceTokens{path: path, now: time.Now, byHash: map[string]DeviceToken{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return d, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read device tokens: %w", err)
	}
	var f deviceTokensFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse device tokens: %w", err)
	}
	for _, t := range f.Tokens {
		if t.Hash != "" {
			d.byHash[t.Hash] = t
		}
	}
	return d, nil
}

// SetClock replaces the time source (tests).
func (d *DeviceTokens) SetClock(now func() time.Time) {
	d.mu.Lock()
	d.now = now
	d.mu.Unlock()
}

// Issue mints a token for installID, replacing any earlier token of the same
// install so a re-pair never leaves a second live credential behind. It returns
// the plaintext token (never stored) and the replaced token hashes.
func (d *DeviceTokens) Issue(installID, deviceName string) (token string, replaced []string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token = deviceTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	next := cloneTokens(d.byHash)
	if installID != "" {
		for h, t := range next {
			if t.InstallID == installID {
				replaced = append(replaced, h)
				delete(next, h)
			}
		}
	}
	h := HashPassword(token)
	next[h] = DeviceToken{Hash: h, InstallID: installID, DeviceName: deviceName, CreatedAt: now, LastUsedAt: now, ExpiresAt: now.Add(DeviceTokenTTL)}
	for len(next) > maxDeviceTokens {
		oldest := ""
		for k, t := range next {
			if oldest == "" || t.LastUsedAt.Before(next[oldest].LastUsedAt) {
				oldest = k
			}
		}
		replaced = append(replaced, oldest)
		delete(next, oldest)
	}
	if err := d.persistLocked(next); err != nil {
		return "", nil, err
	}
	d.byHash = next
	return token, replaced, nil
}

// Verify reports whether token is a live device token, sliding its expiry
// forward. An expired token is deleted.
func (d *DeviceTokens) Verify(token string) (DeviceToken, bool) {
	if len(token) < len(deviceTokenPrefix)+16 || token[:len(deviceTokenPrefix)] != deviceTokenPrefix {
		return DeviceToken{}, false
	}
	h := HashPassword(token)
	d.mu.Lock()
	defer d.mu.Unlock()
	t, ok := d.byHash[h]
	if !ok {
		return DeviceToken{}, false
	}
	now := d.now()
	if !now.Before(t.ExpiresAt) {
		next := cloneTokens(d.byHash)
		delete(next, h)
		if d.persistLocked(next) == nil {
			d.byHash = next
		}
		return DeviceToken{}, false
	}
	if now.Sub(t.LastUsedAt) >= deviceTokenTouchEvery {
		t.LastUsedAt = now
		t.ExpiresAt = now.Add(DeviceTokenTTL)
		next := cloneTokens(d.byHash)
		next[h] = t
		// A failed touch only loses the extension on disk; the token stays valid.
		if d.persistLocked(next) == nil {
			d.byHash = next
		}
	}
	return t, true
}

// deviceTokenLen is the length of an issued token: the prefix plus 32 random
// bytes in unpadded base64url.
var deviceTokenLen = len(deviceTokenPrefix) + base64.RawURLEncoding.EncodedLen(32)

// LooksLikeDeviceToken reports whether token has the exact shape Issue mints.
// Such a bearer that Verify rejects is a revoked or expired pairing, not a
// password guess: it carries 256 random bits, so retrying it learns nothing.
func LooksLikeDeviceToken(token string) bool {
	if len(token) != deviceTokenLen || token[:len(deviceTokenPrefix)] != deviceTokenPrefix {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(token[len(deviceTokenPrefix):])
	return err == nil && len(raw) == 32
}

// RevokeInstall deletes every token of installID and returns their hashes so
// the caller can close connections authenticated with them.
func (d *DeviceTokens) RevokeInstall(installID string) ([]string, error) {
	if installID == "" {
		return nil, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	next := cloneTokens(d.byHash)
	var hashes []string
	for h, t := range next {
		if t.InstallID == installID {
			hashes = append(hashes, h)
			delete(next, h)
		}
	}
	if len(hashes) == 0 {
		return nil, nil
	}
	if err := d.persistLocked(next); err != nil {
		return nil, err
	}
	d.byHash = next
	return hashes, nil
}

// Lookup returns the stored record for a token hash.
func (d *DeviceTokens) Lookup(hash string) (DeviceToken, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	t, ok := d.byHash[hash]
	return t, ok
}

// HasInstall reports whether installID holds a stored token.
func (d *DeviceTokens) HasInstall(installID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, t := range d.byHash {
		if installID != "" && t.InstallID == installID {
			return true
		}
	}
	return false
}

// RevokeHash deletes the single token with hash and reports whether it existed.
func (d *DeviceTokens) RevokeHash(hash string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.byHash[hash]; !ok {
		return false, nil
	}
	next := cloneTokens(d.byHash)
	delete(next, hash)
	if err := d.persistLocked(next); err != nil {
		return false, err
	}
	d.byHash = next
	return true, nil
}

// Count is the number of stored tokens.
func (d *DeviceTokens) Count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.byHash)
}

func cloneTokens(m map[string]DeviceToken) map[string]DeviceToken {
	out := make(map[string]DeviceToken, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (d *DeviceTokens) persistLocked(m map[string]DeviceToken) error {
	if d.path == "" {
		return errors.New("device tokens: no path")
	}
	f := deviceTokensFile{Tokens: make([]DeviceToken, 0, len(m))}
	for _, t := range m {
		f.Tokens = append(f.Tokens, t)
	}
	sort.Slice(f.Tokens, func(i, j int) bool { return f.Tokens[i].CreatedAt.Before(f.Tokens[j].CreatedAt) })
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeFile0600(d.path, b)
}

// writeFile0600 atomically writes b to path with mode 0600 (temp file + rename).
func writeFile0600(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tok-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
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
	return os.Rename(name, path)
}
