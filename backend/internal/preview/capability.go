package preview

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
)

// A preview origin is served without the daemon token (browser tabs, iframes
// and <img> cannot send it), and session ids are short and guessable, so the
// host also carries an unguessable per-session capability: one extra DNS label
// "k-" + 32 hex chars (128 bits) just before ".localhost", for example
// ao-preview.<base32 id>.k-<hex>.localhost. The "-" cannot occur in the base32
// id labels, so the label is unambiguous. The value is
// HMAC-SHA256(key, hostLabel \n sessionID) truncated to 128 bits, so a
// capability for one session never opens another, and one for the workspace
// origin never opens the artifact origin.
//
// The key is the daemon token (SetCapabilityKey, called at startup), so every
// capability is stable across daemon restarts and rotates with the token. With
// no key set (auth disabled, tests) a random per-process key is used.

const (
	capabilityPrefix   = "k-"
	capabilityHexChars = 32
)

var (
	capabilityMu  sync.RWMutex
	capabilityKey []byte
)

// SetCapabilityKey sets the secret preview capabilities derive from. An empty
// key restores the random per-process fallback.
func SetCapabilityKey(key string) {
	capabilityMu.Lock()
	defer capabilityMu.Unlock()
	if key == "" {
		capabilityKey = nil
		return
	}
	capabilityKey = []byte("preview-capability\n" + key)
}

func currentCapabilityKey() []byte {
	capabilityMu.RLock()
	key := capabilityKey
	capabilityMu.RUnlock()
	if key != nil {
		return key
	}
	capabilityMu.Lock()
	defer capabilityMu.Unlock()
	if capabilityKey == nil {
		capabilityKey = make([]byte, 32)
		if _, err := rand.Read(capabilityKey); err != nil {
			panic("preview: no entropy for capability key: " + err.Error())
		}
	}
	return capabilityKey
}

// capabilityLabel is the DNS label that authorizes reading id's files on the
// origin whose first label is hostLabel.
func capabilityLabel(hostLabel, id string) string {
	mac := hmac.New(sha256.New, currentCapabilityKey())
	mac.Write([]byte(hostLabel + "\n" + id))
	return capabilityPrefix + hex.EncodeToString(mac.Sum(nil)[:capabilityHexChars/2])
}

func isCapabilityLabel(l string) bool {
	return len(l) == len(capabilityPrefix)+capabilityHexChars && strings.HasPrefix(l, capabilityPrefix)
}

func capabilityMatches(hostLabel, id, presented string) bool {
	return hmac.Equal([]byte(capabilityLabel(hostLabel, id)), []byte(presented))
}
