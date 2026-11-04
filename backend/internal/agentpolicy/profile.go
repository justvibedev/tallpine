package agentpolicy

import (
	"bytes"
	"encoding/json"
)

// ProfileHardened is the only managed profile. Set by the top-level managed key
// "profile": "hardened", it locks a strict floor on every relevant rule and
// makes launches fail closed when the session cannot be fenced off Tallpine
// state (see protectstate.Outcome.Required).
const ProfileHardened = "hardened"

// SourceManagedProfile marks a field the managed profile set to its floor.
const SourceManagedProfile Source = "managed_profile"

// DevModeRule is the devMode rule's name (rule packages import this one).
const DevModeRule = "devMode"

// profileFloor is one field the hardened profile locks.
type profileFloor struct {
	rule, field string
	value       json.RawMessage
}

// hardenedFloors are the hardened profile's locked values. Rule and field names
// are literals because rule packages import this one; the rules registry test
// checks every entry names a real enum or bool field.
var hardenedFloors = []profileFloor{
	{"permissionCap", "max", json.RawMessage(`"auto"`)},
	{"envFilter", "mode", json.RawMessage(`"allowlist"`)},
	{"githubCreds", "mode", json.RawMessage(`"helper"`)},
	{"protectState", "mode", json.RawMessage(`"on"`)},
	{"repoAgentConfig", "enabled", json.RawMessage(`false`)},
	{"orchestratorApprovals", "enabled", json.RawMessage(`false`)},
	{"crossProjectOrchestration", "enabled", json.RawMessage(`false`)},
	{DevModeRule, "enabled", json.RawMessage(`false`)},
}

// HardenedFloorFields lists "rule.field" for every field the profile locks.
func HardenedFloorFields() []string {
	out := make([]string, 0, len(hardenedFloors))
	for _, f := range hardenedFloors {
		out = append(out, f.rule+"."+f.field)
	}
	return out
}

// parseProfile reads the managed "profile" value. Any present value applies the
// hardened profile: an unknown or malformed name is the strictest reading of
// what the administrator meant, with a warning.
func parseProfile(raw json.RawMessage, present bool) (string, string) {
	if !present {
		return "", ""
	}
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		return ProfileHardened, "managed policy profile is invalid; the hardened profile is applied"
	}
	if name != ProfileHardened {
		return ProfileHardened, "managed policy profile \"" + name + "\" is unknown; the hardened profile is applied"
	}
	return ProfileHardened, ""
}

// applyProfileFloors overlays the profile's floors on the explicit managed
// values. An explicit value at least as strict as the floor is kept (source
// managed); a looser one is dropped with a warning and the floor applies.
func applyProfileFloors(rules []Rule, l *layer) {
	fields := map[string]Field{}
	for _, r := range rules {
		for _, f := range r.Schema().Fields {
			fields[r.Name()+"."+f.Name] = f
		}
	}
	if l.profileSet == nil {
		l.profileSet = map[string]bool{}
	}
	for _, fl := range hardenedFloors {
		key := fl.rule + "." + fl.field
		f, known := fields[key]
		if !known {
			continue // rule not registered in this engine (tests)
		}
		if explicit, ok := l.get(fl.rule, fl.field); ok {
			if atLeastAsStrict(f, explicit, fl.value) {
				continue
			}
			l.warnings = append(l.warnings, "managed policy "+key+" is looser than the hardened profile allows; ignored")
		}
		setValue(l.values, fl.rule, fl.field, bytes.Clone(fl.value))
		l.profileSet[key] = true
	}
}

// atLeastAsStrict compares v with floor: enums by declared order, bools by
// "false is stricter" (every profile bool floor turns a capability off).
func atLeastAsStrict(f Field, v, floor json.RawMessage) bool {
	if f.Kind == KindBool {
		return bytes.Equal(bytes.TrimSpace(v), []byte("false")) || bytes.Equal(bytes.TrimSpace(v), bytes.TrimSpace(floor))
	}
	sv, sf := f.strictness(v), f.strictness(floor)
	return sv >= 0 && sv >= sf
}
