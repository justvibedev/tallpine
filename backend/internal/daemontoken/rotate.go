package daemontoken

import (
	"fmt"
	"os"
	"path/filepath"
)

// SessionKeyFileName holds the secret that scoped agent session tokens are
// MACed under. It is separate from the install token so the install token can
// rotate on every daemon start while agents that outlive the daemon (tmux,
// persistent chat hosts) keep working tokens.
const SessionKeyFileName = "session.key"

// SessionKeyPath returns the session-key file next to the install token at
// tokenPath.
func SessionKeyPath(tokenPath string) string {
	return filepath.Join(filepath.Dir(tokenPath), SessionKeyFileName)
}

// RotateOnStart is the daemon-start entry point. It returns the session-key
// secret and then replaces the install token at tokenPath with a fresh one.
//
// The session key is read first so an existing install's key can be seeded
// from the current install token: tokens already handed to running agents
// were derived from it, so this keeps them valid across the upgrade and every
// later rotation. Only the install token (CLI, desktop app) is rotated; the
// CLI re-reads the file per call and the app re-reads it when the daemon
// reports ready, so both pick the new token up without a restart.
func RotateOnStart(tokenPath string) (installToken, sessionKey string, err error) {
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		return "", "", fmt.Errorf("create daemon token dir: %w", err)
	}
	sessionKey, err = ensureSessionKey(tokenPath)
	if err != nil {
		return "", "", err
	}
	installToken, err = Rotate(tokenPath)
	if err != nil {
		return "", "", err
	}
	return installToken, sessionKey, nil
}

// Rotate replaces the install token at path with a fresh one: written 0600 to
// a temp file and renamed over the old file, so a concurrent reader sees the
// old or the new token, never a partial one. Whatever sat at path before (a
// widened mode, a symlink) is replaced, not followed.
func Rotate(path string) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create daemon token dir: %w", err)
	}
	fresh, err := generate()
	if err != nil {
		return "", err
	}
	tmp, err := writeTemp(dir, fresh)
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("publish rotated daemon token: %w", err)
	}
	return fresh, nil
}

// ensureSessionKey returns the stable session-key secret, creating it when
// absent or unusable. A missing file is seeded from the current install token
// when that is well formed (migration), else a fresh random key.
func ensureSessionKey(tokenPath string) (string, error) {
	keyPath := SessionKeyPath(tokenPath)
	if key, state := inspect(keyPath); state == stateValid {
		return key, nil
	}
	key, _ := inspect(tokenPath)
	if key == "" {
		// An install token with widened modes is not trusted as a seed.
		fresh, err := generate()
		if err != nil {
			return "", err
		}
		key = fresh
	}
	tmp, err := writeTemp(filepath.Dir(keyPath), key)
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmp, keyPath); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("publish session key: %w", err)
	}
	return key, nil
}

// RotateSessionKey replaces the session key with a fresh random one, written
// 0600 via temp file and rename. Every scoped token minted under the old key
// stops verifying, so the caller must only do this when no agent holds one
// (no live sessions).
func RotateSessionKey(tokenPath string) (string, error) {
	keyPath := SessionKeyPath(tokenPath)
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return "", fmt.Errorf("create daemon token dir: %w", err)
	}
	fresh, err := generate()
	if err != nil {
		return "", err
	}
	tmp, err := writeTemp(filepath.Dir(keyPath), fresh)
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmp, keyPath); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("publish rotated session key: %w", err)
	}
	return fresh, nil
}
