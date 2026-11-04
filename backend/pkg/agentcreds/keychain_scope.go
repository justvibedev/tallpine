package agentcreds

import (
	"crypto/sha256"
	"encoding/hex"

	"golang.org/x/text/unicode/norm"
)

// keychainServices are the service names of one Claude Code login in the macOS
// keychain.
type keychainServices struct {
	credentials string
	managedKey  string
}

// keychainServicesFor names the keychain items of the login a launch with
// opts.CommandEnv uses. Claude Code appends "-" and the first 8 hex digits of
// the SHA-256 of the NFC-normalised config folder to the service name whenever
// a folder is set, so an agent account's login is a different item from the
// device's. With no folder in the launch env these are the device items.
//
// Derived from Claude Code 2.1.311 (kW in the keychain module of the shipped
// binary): the folder is CLAUDE_SECURESTORAGE_CONFIG_DIR when that is set (even
// to ""), else CLAUDE_CONFIG_DIR; the suffix is empty when that value is empty;
// the service is "Claude Code" + "-credentials" (the managed key omits that
// part) + suffix. The production OAuth file suffix is empty.
func keychainServicesFor(opts ResolveOptions) keychainServices {
	suffix := keychainFolderSuffix(opts.CommandEnv)
	return keychainServices{
		credentials: keychainServiceCredentials + suffix,
		managedKey:  keychainServiceManagedKey + suffix,
	}
}

func keychainFolderSuffix(env map[string]string) string {
	folder, ok := env["CLAUDE_SECURESTORAGE_CONFIG_DIR"]
	if !ok {
		folder = env["CLAUDE_CONFIG_DIR"]
	}
	if folder == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(norm.NFC.String(folder)))
	return "-" + hex.EncodeToString(sum[:])[:8]
}
