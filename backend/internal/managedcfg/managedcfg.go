// Package managedcfg holds the managed (MDM) values that set and lock
// registry settings. The settings registry decides which keys are lockable and
// the daemon builds a Locks from the managed file; services that own a locked
// setting import only this package (the registry imports the services, so they
// cannot import it).
package managedcfg

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// CodeLocked is the API error code of a write to a locked setting.
const CodeLocked = "SETTING_LOCKED"

// Provider returns the current locked values by registry key. A nil value
// means the key is locked but the managed value could not be applied (an
// unreadable file or an invalid value): the stored value is kept and stays
// read-only, so a broken file never loosens a lock.
type Provider func() map[string]json.RawMessage

// Locks answers which settings the managed file locks. The zero value and a
// nil *Locks lock nothing.
type Locks struct{ provider Provider }

// New wraps a provider.
func New(p Provider) *Locks { return &Locks{provider: p} }

func (l *Locks) values() map[string]json.RawMessage {
	if l == nil || l.provider == nil {
		return nil
	}
	return l.provider()
}

// Value reports whether key is locked and the managed value (nil when locked
// without a usable value).
func (l *Locks) Value(key string) (json.RawMessage, bool) {
	v, ok := l.values()[key]
	return v, ok
}

// Locked reports whether the managed file locks key.
func (l *Locks) Locked(key string) bool {
	_, ok := l.values()[key]
	return ok
}

// Refuse returns a SETTING_LOCKED error naming the locked keys among keys, or
// nil when none is locked.
func (l *Locks) Refuse(keys ...string) error {
	var locked []string
	for _, k := range keys {
		if l.Locked(k) {
			locked = append(locked, k)
		}
	}
	if len(locked) == 0 {
		return nil
	}
	sort.Strings(locked)
	return apierr.Conflict(CodeLocked, "managed by your organization: "+strings.Join(locked, ", ")+" cannot be changed", map[string]any{"keys": locked})
}

// Equal reports whether two JSON documents are the same value.
func Equal(a, b json.RawMessage) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return false
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}
