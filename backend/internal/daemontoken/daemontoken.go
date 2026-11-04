// Package daemontoken owns the per-install secret that authenticates callers
// of the daemon's loopback listener (see docs/adr/0007-authenticated-loopback-listener.md).
//
// The token lives in daemon.token next to running.json: the run-file is the
// one location the daemon, the CLI (including hook commands and agent-side
// `tallpine` calls), and the Electron supervisor already agree on, and it moves with
// TALLPINE_RUN_FILE and the TALLPINE_DATA_DIR-based dev/lab isolation. Keying it on the
// state dir instead would split the desktop daemon (which always runs with
// TALLPINE_DATA_DIR=~/.tallpine/data) from a plain-shell CLI (which resolves ~/.tallpine).
//
// The daemon rotates the file on every start (RotateOnStart); clients only
// read it (Read) and must re-read after a daemon restart. Agent shells are
// unaffected: their scoped session tokens are keyed on session.key, which
// survives rotation (see rotate.go).
package daemontoken

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"tallpine.eu/tallpine/backend/internal/buildinfo"
)

const (
	// FileName is the token file's name inside the run-file directory.
	FileName = "daemon.token"
	// Header carries the token on every loopback HTTP and WebSocket request.
	// It is deliberately not Authorization: Bearer, which already means the
	// LAN listener's password.
	Header = "X-VO-Token"
	// AuthEnv is the daemon-side escape hatch: TALLPINE_DAEMON_AUTH=off disables
	// enforcement for debugging. Any other value (or unset) enforces.
	AuthEnv = "TALLPINE_DAEMON_AUTH"
	// UnsafeDevEnv must also be "1" for AuthEnv=off to take effect, so a
	// stray user-level TALLPINE_DAEMON_AUTH=off cannot silently disable auth.
	UnsafeDevEnv = "TALLPINE_DAEMON_AUTH_UNSAFE_DEV"

	tokenBytes = 32
)

// ErrMissing reports that no usable token file exists at the path.
var ErrMissing = errors.New("daemon token file is missing or empty")

// Path returns the token file location for the daemon whose handshake file
// is runFilePath.
func Path(runFilePath string) string {
	return filepath.Join(filepath.Dir(runFilePath), FileName)
}

// AuthOffHonored reports whether auth stays off: TALLPINE_DAEMON_AUTH=off and the
// second explicit flag TALLPINE_DAEMON_AUTH_UNSAFE_DEV=1 must both be set, and only
// an unstamped dev/test build honors them. A release build ignores both, so
// user-level env (launchctl setenv) cannot disable auth on an installed app.
func AuthOffHonored(authValue, unsafeDevValue string) bool {
	return buildinfo.IsDevBuild() && AuthDisabled(authValue) && strings.TrimSpace(unsafeDevValue) == "1"
}

// AuthDisabled reports whether the TALLPINE_DAEMON_AUTH escape hatch is set to off.
func AuthDisabled(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "off")
}

// Read returns the token stored at path for a client to send. A missing or
// empty file yields ErrMissing.
func Read(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // Tallpine-owned path derived from the run-file location.
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrMissing
	}
	if err != nil {
		return "", fmt.Errorf("read daemon token: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", ErrMissing
	}
	return token, nil
}

// Ensure returns the install token at path, creating it when it is missing,
// malformed, not a regular file, or readable by anyone but the owner. The
// file is written to a temp file with mode 0600 (chmod'ed explicitly, so the
// process umask cannot widen or narrow it) and then published atomically:
// hard-linked into place when no file exists, so two racing daemons converge
// on one token, or renamed over an unusable one.
func Ensure(path string) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create daemon token dir: %w", err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		token, state := inspect(path)
		if state == stateValid {
			return token, nil
		}
		fresh, err := generate()
		if err != nil {
			return "", err
		}
		tmp, err := writeTemp(dir, fresh)
		if err != nil {
			return "", err
		}
		if state == stateAbsent {
			err = os.Link(tmp, path)
			_ = os.Remove(tmp)
			if errors.Is(err, fs.ErrExist) {
				continue // another daemon won the race; adopt its token
			}
			if err == nil {
				return fresh, nil
			}
			// Filesystems without hard links fall back to a replace below.
			if tmp, err = writeTemp(dir, fresh); err != nil {
				return "", err
			}
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			return "", fmt.Errorf("publish daemon token: %w", err)
		}
		return fresh, nil
	}
	return "", errors.New("daemon token: could not settle the token file after concurrent updates")
}

// Restore republishes a running daemon's token at path when the file has
// gone missing, so clients started afterwards can still authenticate. An
// existing file is never overwritten.
func Restore(path, token string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create daemon token dir: %w", err)
	}
	tmp, err := writeTemp(dir, token)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()
	if err := os.Link(tmp, path); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("restore daemon token: %w", err)
	}
	return nil
}

// Equal compares a presented token with the expected one in constant time.
// Both sides are hashed first so neither the comparison nor an early length
// mismatch reveals anything about the expected token.
func Equal(presented, expected string) bool {
	if expected == "" {
		return false
	}
	a := sha256.Sum256([]byte(presented))
	b := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

type fileState int

const (
	stateAbsent fileState = iota
	stateUnusable
	stateValid
)

func inspect(path string) (string, fileState) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", stateAbsent
	}
	if err != nil || !info.Mode().IsRegular() {
		return "", stateUnusable
	}
	// Windows reports synthetic permission bits; the file sits in the user's
	// profile, whose ACL already restricts it to the owner.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", stateUnusable
	}
	token, err := Read(path)
	if err != nil || !wellFormed(token) {
		return "", stateUnusable
	}
	return token, stateValid
}

func wellFormed(token string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(raw) == tokenBytes
}

func generate() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate daemon token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func writeTemp(dir, token string) (string, error) {
	f, err := os.CreateTemp(dir, ".daemon-token-*")
	if err != nil {
		return "", fmt.Errorf("create temp daemon token: %w", err)
	}
	name := f.Name()
	fail := func(err error) (string, error) {
		_ = f.Close()
		_ = os.Remove(name)
		return "", fmt.Errorf("write temp daemon token: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("close temp daemon token: %w", err)
	}
	return name, nil
}
