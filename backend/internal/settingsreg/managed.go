package settingsreg

import (
	"encoding/json"
	"sort"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/managedcfg"
)

// ManagedFileKey is a lockable key the managed file carries and the desktop app
// (Electron) reads itself, because the app updates before the daemon runs. The
// daemon neither stores nor serves these; they are listed here so the one key
// list (managed-keys.json) covers them.
type ManagedFileKey struct {
	// Key is the registry-style name.
	Key string `json:"key"`
	// PlistKey is the top-level key in the managed file.
	PlistKey string `json:"plistKey"`
	Kind     Kind   `json:"kind"`
}

// ElectronManagedKeys are the auto-update keys read by frontend/src/main/update-policy.ts.
var ElectronManagedKeys = []ManagedFileKey{
	{Key: "app.update.enabled", PlistKey: "AutoUpdate", Kind: KindBool},
	{Key: "app.update.channel", PlistKey: "AutoUpdateChannel", Kind: KindEnum},
	{Key: "app.update.pinnedVersion", PlistKey: "AutoUpdatePinnedVersion", Kind: KindString},
}

// ManagedKeys lists every key the managed file can set, in a stable order: the
// daemon's lockable settings (inside the file's "settings" dictionary, except
// policy.*, which live under "rules") then the desktop app's.
func (r *Registry) ManagedKeys() []ManagedFileKey {
	var out []ManagedFileKey
	for _, s := range r.order {
		if s.MDMLockable && s.Scope != ScopePolicy {
			out = append(out, ManagedFileKey{Key: string(s.Key), PlistKey: "settings." + string(s.Key), Kind: s.Kind})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return append(out, ElectronManagedKeys...)
}

// ManagedLocks builds the Locks the daemon's services consult. A lockable key
// the file sets is locked at that value; one set to an invalid value is locked
// at its stored value. If the file is unreadable every lockable key is locked
// at its stored value (fail closed). Without a file nothing is locked.
func (r *Registry) ManagedLocks(read func() agentpolicy.ManagedSettings) *managedcfg.Locks {
	return managedcfg.New(func() map[string]json.RawMessage {
		ms := read()
		if !ms.Unreadable && len(ms.Values) == 0 {
			return nil
		}
		out := map[string]json.RawMessage{}
		for _, s := range r.order {
			if !s.MDMLockable || s.Scope == ScopePolicy {
				continue
			}
			raw, set := ms.Values[string(s.Key)]
			switch {
			case set && s.ValueFits(raw):
				out[string(s.Key)] = raw
			case set || ms.Unreadable:
				out[string(s.Key)] = nil
			}
		}
		return out
	})
}

// ValueFits reports whether raw is a valid value of the setting.
func (s Setting) ValueFits(raw json.RawMessage) bool { return s.ValidateValue(raw) == nil }
