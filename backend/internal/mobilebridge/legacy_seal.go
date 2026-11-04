package mobilebridge

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// legacyKeyFile holds the per-install key that seals the legacy password hash.
// It sits beside the mobile config in the same 0700 directory, mode 0600, so a
// copied config.json or backup of it alone does not allow an offline guess of
// the 8-character password.
const legacyKeyFile = "legacy-key"

const sealedPrefix = "hmac1:"

// IsSealedLegacyHash reports whether h is in the keyed form.
func IsSealedLegacyHash(h string) bool { return strings.HasPrefix(h, sealedPrefix) }

// SealLegacyHash wraps an unkeyed password hash (HashPassword output) under key.
func SealLegacyHash(key []byte, plainHash string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(plainHash))
	return sealedPrefix + hex.EncodeToString(mac.Sum(nil))
}

// LegacyPasswordMatches checks pw against a stored legacy hash: keyed when the
// hash is sealed, plain SHA-256 otherwise (key may then be nil).
func LegacyPasswordMatches(key []byte, stored, pw string) bool {
	if !IsSealedLegacyHash(stored) {
		return PasswordMatches(stored, pw)
	}
	if len(key) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(SealLegacyHash(key, HashPassword(pw)))) == 1
}

// LoadLegacyKey returns the per-install key next to configPath, creating it
// (0600) when missing.
func LoadLegacyKey(configPath string) ([]byte, error) {
	dir := filepath.Dir(configPath)
	path := filepath.Join(dir, legacyKeyFile)
	if b, err := os.ReadFile(path); err == nil && len(b) == 32 {
		return b, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir mobile dir: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".legacy-key-*.tmp")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write(key); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	// Link, not rename: if another process created the key first, use theirs.
	if err := os.Link(tmp.Name(), path); err != nil {
		if b, rerr := os.ReadFile(path); rerr == nil && len(b) == 32 {
			return b, nil
		}
		return nil, err
	}
	return key, nil
}

// sealLegacyState re-stores an unkeyed LegacyPasswordHash in keyed form. It
// reports whether s changed; on any key error the hash is left as it was.
func sealLegacyState(configPath string, s *State) bool {
	if s.LegacyPasswordHash == "" || IsSealedLegacyHash(s.LegacyPasswordHash) {
		return false
	}
	key, err := LoadLegacyKey(configPath)
	if err != nil {
		return false
	}
	s.LegacyPasswordHash = SealLegacyHash(key, s.LegacyPasswordHash)
	return true
}
