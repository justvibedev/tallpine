package agentpolicy

import (
	"bytes"
	"encoding/json"
)

// Names of the Security mode rule and its fields. The securitymode package
// declares the same literals; they live here too because rule packages import
// this one (the rules registry test checks they name real fields).
const (
	SecurityModeRule        = "securityMode"
	SecurityModeEnabled     = "enabled"
	SecurityModeAllowDocker = "allowDocker"
)

// securityFloors are the values Security mode raises to when it is on. Only the
// three tighten-only fields: permissionCap, orchestratorApprovals and
// crossProjectOrchestration stay as the user set them.
var securityFloors = []profileFloor{
	{"protectState", "mode", json.RawMessage(`"on"`)},
	{"envFilter", "mode", json.RawMessage(`"allowlist"`)},
	{"githubCreds", "mode", json.RawMessage(`"helper"`)},
}

// SecurityFloorFields lists "rule.field" for every field Security mode raises.
func SecurityFloorFields() []string {
	out := make([]string, 0, len(securityFloors))
	for _, f := range securityFloors {
		out = append(out, f.rule+"."+f.field)
	}
	return out
}

// applySecurityMode resolves the Security mode switch across rule states and,
// when it is on, raises the floors. The hardened profile implies on. A looser
// user or project value is raised to the floor (source security_mode); a
// stricter one is kept. A value locked by the managed file, the profile or the
// environment is never touched: an administrator's explicit choice wins over
// the user-level switch.
func applySecurityMode(rules []Rule, out *resolved) {
	sm, ok := out.states[SecurityModeRule]
	if !ok {
		return
	}
	en, ok := sm.fields[SecurityModeEnabled]
	if !ok {
		return
	}
	// allowDocker is a per-project opt-in: an install-wide value would open the
	// Docker socket for every project, and under the hardened profile only the
	// managed file may open it. Anything else is dropped with a warning.
	hardened := out.profile == ProfileHardened
	if d, ok := sm.fields[SecurityModeAllowDocker]; ok && (d.Source == SourceUser || (hardened && !d.Locked)) && boolValue(d.Value) {
		sm.fields[SecurityModeAllowDocker] = FieldState{Value: json.RawMessage(`false`), Source: SourceDefault}
		sm.settings[SecurityModeAllowDocker] = json.RawMessage(`false`)
		out.warnings = append(out.warnings, "securityMode.allowDocker is ignored here: it is per project and not allowed install-wide or under the hardened profile")
	}
	if hardened && (!en.Locked || !boolValue(en.Value)) {
		if en.Locked {
			out.warnings = append(out.warnings, "securityMode.enabled is off but the hardened profile implies on; on is applied")
		}
		en = FieldState{Value: json.RawMessage(`true`), Source: SourceManagedProfile, Locked: true}
		sm.fields[SecurityModeEnabled] = en
		sm.settings[SecurityModeEnabled] = en.Value
	}
	if !boolValue(en.Value) {
		return
	}
	// Dev mode (owner-set, install-wide) means the owner is developing and wants
	// speed: the floors are not raised, as if Security mode were off. A hardened
	// profile forces Dev mode off (and locked values are left as resolved),
	// and the managed file can force Dev mode off too.
	if dm, ok := out.states[DevModeRule]; ok && !hardened && boolValue(dm.fields["enabled"].Value) {
		return
	}
	defs := map[string]Field{}
	for _, r := range rules {
		for _, f := range r.Schema().Fields {
			defs[r.Name()+"."+f.Name] = f
		}
	}
	for _, fl := range securityFloors {
		f, known := defs[fl.rule+"."+fl.field]
		st, have := out.states[fl.rule]
		if !known || !have {
			continue
		}
		cur := st.fields[fl.field]
		if cur.Locked || atLeastAsStrict(f, cur.Value, fl.value) {
			continue
		}
		cur = FieldState{Value: bytes.Clone(fl.value), Source: SourceSecurityMode}
		st.fields[fl.field] = cur
		st.settings[fl.field] = cur.Value
	}
}

func boolValue(raw json.RawMessage) bool {
	var v bool
	_ = json.Unmarshal(raw, &v)
	return v
}

// securityModeView is the resolved switch for Describe.
func securityModeView(res resolved) SecurityModeView {
	st := res.states[SecurityModeRule].fields[SecurityModeEnabled]
	return SecurityModeView{Enabled: boolValue(st.Value), Source: st.Source, Locked: st.Locked}
}
