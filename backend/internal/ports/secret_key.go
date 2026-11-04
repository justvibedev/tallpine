package ports

import (
	"context"
	"errors"
)

// SecretKeyStore holds the 256-bit key that encrypts project env values at rest
// under the managed hardened profile. Key returns the key, creating it on first
// use; it wraps ErrSecretKeyUnavailable when the key cannot be read or made
// (Keychain locked, access denied, unsupported OS).
type SecretKeyStore interface {
	Key(ctx context.Context) ([]byte, error)
}

// SecretKeyRecreator is implemented by a SecretKeyStore that can replace any
// existing key item with a fresh key it creates itself. The codec uses it on
// first use, so a key planted before Tallpine created one is never adopted.
type SecretKeyRecreator interface {
	Recreate(ctx context.Context) ([]byte, error)
}

// ErrSecretKeyUnavailable is wrapped by every SecretKeyStore failure.
var ErrSecretKeyUnavailable = errors.New("project env encryption key unavailable")

// CodeProjectEnvLocked is the API error code for a project whose encrypted env
// cannot be decrypted (or encrypted) because the key is unavailable.
const CodeProjectEnvLocked = "PROJECT_ENV_LOCKED"

// ProjectEnvLockedMessage is the user-facing text of a PROJECT_ENV_LOCKED error.
const ProjectEnvLockedMessage = "This project's environment variables are encrypted and Tallpine cannot read the key from the macOS Keychain. Unlock the Keychain (or allow Tallpine access) and try again"

// ErrProjectEnvLocked is matched by errors.Is when a project's env is
// encrypted and the key is unavailable: launches of that project and writes to
// it fail closed rather than run without, or rewrite as plaintext, its env.
var ErrProjectEnvLocked = errors.New("project environment variables are encrypted and the key is unavailable")
