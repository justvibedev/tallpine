package settingswrite

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// securityModeKey is the Security mode switch; an agent may only turn it on.
// allowDocker is user-only (policyWriters gives it no agent writer).
const securityModeKey = settingsreg.Key("policy." + agentpolicy.SecurityModeRule + "." + agentpolicy.SecurityModeEnabled)

// agentMayChange admits only an orchestrator changing its own project. App
// settings and the install-wide agent policy stay with the user.
func agentMayChange(agent requestscope.Agent, project domain.ProjectID) error {
	if agent.Role != requestscope.RoleOrchestrator {
		return &Error{Code: CodeNotPermitted, Message: "only the user and orchestrators may change settings"}
	}
	if agent.DevMode {
		return nil
	}
	if project == "" || project != agent.ProjectID {
		return &Error{Code: CodeNotPermitted, Message: "an orchestrator may change only its own project's settings (project " + string(agent.ProjectID) + ")"}
	}
	return nil
}

// agentMayWrite returns why an orchestrator may not write def, or "". An
// agent may tighten agent policy, never loosen it: enum values are ordered
// from least to most strict. Writing an object never changes a nested key
// only the user may write (project.claudePlugins.reviewer), even in Dev mode.
func agentMayWrite(agent requestscope.Agent, reg *settingsreg.Registry, def settingsreg.Setting, cur, next json.RawMessage) string {
	if why := userOnlySubKeyChanged(reg, def, cur, next); why != "" {
		return why
	}
	if agent.DevMode {
		// Dev mode: anything the owner can write, except Dev mode itself.
		if strings.HasPrefix(string(def.Key), "policy."+agentpolicy.DevModeRule+".") {
			return "only the user may change Dev mode"
		}
		if def.Scope != settingsreg.ScopePolicy && !def.CanWrite(settingsreg.ActorOrchestrator) {
			return "only the user may change it"
		}
		if def.Sensitive {
			return keepsHiddenEntries(cur, next) // it reads these redacted, even in Dev mode
		}
		return ""
	}
	if !def.CanWrite(settingsreg.ActorOrchestrator) {
		return "only the user may change it"
	}
	if def.Sensitive {
		return keepsHiddenEntries(cur, next)
	}
	if def.Scope != settingsreg.ScopePolicy {
		return ""
	}
	if def.Key == securityModeKey {
		// An agent may turn Security mode on, never off.
		if string(bytes.TrimSpace(next)) != "true" {
			return "an agent may only turn Security mode on"
		}
		return ""
	}
	var from, to string
	if json.Unmarshal(cur, &from) != nil || json.Unmarshal(next, &to) != nil ||
		slices.Index(def.Enum, to) < slices.Index(def.Enum, from) {
		return "an agent may only tighten agent policy"
	}
	return ""
}

// keepsHiddenEntries lets an agent add to a sensitive map it reads redacted
// (project.env) but never drop or change an entry it cannot see: the change
// log redacts both sides, so a lost secret could not be restored.
func keepsHiddenEntries(cur, next json.RawMessage) string {
	var before, after map[string]json.RawMessage
	if json.Unmarshal(cur, &before) != nil || len(before) == 0 {
		return ""
	}
	_ = json.Unmarshal(next, &after)
	for name, value := range before {
		if kept, ok := after[name]; !ok || !bytes.Equal(kept, value) {
			return "an agent may only add entries to it; " + name + " would be removed or changed"
		}
	}
	return ""
}

// userOnlySubKeyChanged returns why writing object def from cur to next
// changes a registered nested key no orchestrator may write, or "".
func userOnlySubKeyChanged(reg *settingsreg.Registry, def settingsreg.Setting, cur, next json.RawMessage) string {
	if def.Kind != settingsreg.KindObject {
		return ""
	}
	var from, to any
	_ = json.Unmarshal(cur, &from)
	_ = json.Unmarshal(next, &to)
	prefix := string(def.Key) + "."
	for _, sub := range reg.All() {
		rest, ok := strings.CutPrefix(string(sub.Key), prefix)
		if !ok || sub.CanWrite(settingsreg.ActorOrchestrator) {
			continue
		}
		path := strings.Split(rest, ".")
		if !reflect.DeepEqual(jsonAt(from, path), jsonAt(to, path)) {
			return "only the user may change " + string(sub.Key)
		}
	}
	return ""
}

// jsonAt is the value at path in a decoded JSON document, nil when absent or
// empty, so an absent role and an empty one compare equal.
func jsonAt(doc any, path []string) any {
	for _, p := range path {
		m, ok := doc.(map[string]any)
		if !ok {
			return nil
		}
		doc = m[p]
	}
	if m, ok := doc.(map[string]any); ok && len(m) == 0 {
		return nil
	}
	return doc
}
