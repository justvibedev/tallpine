// Package secretkey holds the key that encrypts project env values at rest
// under the managed hardened profile (ports.SecretKeyStore).
//
// On macOS the key is a generic-password item in the login Keychain that the
// daemon creates itself through the Security framework, so the item's access
// list trusts only the daemon's own code identity. It is never created or read
// with /usr/bin/security, whose access list any same-user process could use
// without a prompt. Elsewhere (or a build without cgo) the key is unavailable
// and encrypted env fails closed.
package secretkey

// Item identity in the Keychain.
const (
	Service = "ee.justdev.tallpine.project-env"
	Account = "aes-256-gcm-v1"
)

// Options selects the keychain. Keychain is the path of a keychain file; empty
// means the user's default (login) keychain. Tests pass a scratch keychain.
type Options struct {
	Keychain string
	// NoUI fails instead of showing a Keychain prompt (tests, headless runs).
	// It turns off Keychain user interaction for the whole process.
	NoUI bool
}
