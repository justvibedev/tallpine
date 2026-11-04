package settingsreg

import (
	"encoding/json"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/brand"
)

// JamfSchema renders the Jamf Pro "Custom Schema" for the managed preference
// domain: the top-level keys the daemon and desktop app read (schemaVersion,
// the AutoUpdate* keys), the lockable settings under "settings", and every
// agent-policy field under "rules". It is generated from the registry so it
// cannot drift from what the app accepts; the output is golden-tested.
func (r *Registry) JamfSchema() ([]byte, error) {
	settings := map[string]any{}
	rules := map[string]map[string]any{}
	for _, s := range r.order {
		if !s.MDMLockable {
			continue
		}
		prop := jamfProperty(s)
		if s.Scope != ScopePolicy {
			settings[string(s.Key)] = prop
			continue
		}
		rule, field, _ := strings.Cut(strings.TrimPrefix(string(s.Key), "policy."), ".")
		if rules[rule] == nil {
			rules[rule] = map[string]any{}
		}
		rules[rule][field] = prop
	}
	ruleProps := map[string]any{}
	for name, fields := range rules {
		ruleProps[name] = map[string]any{"title": name, "type": "object", "properties": fields}
	}
	props := map[string]any{
		"schemaVersion": map[string]any{"title": "Schema version", "description": "Managed file format version.", "type": "integer", "default": agentpolicy.ManagedSchemaVersion},
		"profile":       map[string]any{"title": "Profile", "description": "Hardened profile; see docs/agent-policy.md.", "type": "string", "enum": []string{string(agentpolicy.ProfileHardened)}},
		"rules":         map[string]any{"title": "Agent policy rules", "description": "Agent policy; see docs/agent-policy.md.", "type": "object", "properties": ruleProps},
		"settings":      map[string]any{"title": "Locked settings", "description": "Settings the organization sets and locks.", "type": "object", "properties": settings},
	}
	for _, k := range ElectronManagedKeys {
		var p map[string]any
		switch k.Kind {
		case KindBool:
			p = map[string]any{"type": "boolean"}
		case KindEnum:
			p = map[string]any{"type": "string", "enum": []string{"stable", "beta"}}
		default:
			p = map[string]any{"type": "string"}
		}
		p["title"] = k.PlistKey
		p["description"] = "Desktop app update policy (" + k.Key + ")."
		props[k.PlistKey] = p
	}
	doc := map[string]any{
		"title":       brand.BundleID,
		"description": "Tallpine managed preferences.",
		"__version":   "1.0",
		"__feedback":  "https://github.com/justvibedev/tallpine",
		"type":        "object",
		"properties":  props,
	}
	out, err := json.MarshalIndent(doc, "", "\t")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func jamfProperty(s Setting) map[string]any {
	p := map[string]any{"title": string(s.Key), "description": s.Description}
	switch s.Kind {
	case KindBool:
		p["type"] = "boolean"
	case KindInt:
		p["type"] = "integer"
		if s.Min != nil {
			p["minimum"] = *s.Min
		}
		if s.Max != nil {
			p["maximum"] = *s.Max
		}
	case KindEnum:
		p["type"] = "string"
		p["enum"] = s.Enum
	case KindString, KindText:
		p["type"] = "string"
	case KindStringList:
		p["type"] = "array"
		p["items"] = map[string]any{"type": "string"}
	default:
		p["type"] = "object"
	}
	return p
}
