package controllers

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/agentsettings"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	"tallpine.eu/tallpine/backend/internal/service/settingsaudit"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// agentSettingsAllowed applies the agentSettings policy (B202) to an agent's
// write of project keys through the project routes, as settingswrite does for
// PATCH /settings/values, so the organization switch covers every agent path.
// It answers 403 and returns false when a key is refused. Without a policy
// engine it refuses: agent writes never go unchecked.
func (c *ProjectsController) agentSettingsAllowed(w http.ResponseWriter, r *http.Request, project domain.ProjectID, keys []string) bool {
	if len(keys) == 0 {
		return true
	}
	// Human-only keys (project.planAutoApprove) are refused to every agent,
	// whatever the agent policy says.
	if humanOnly := humanOnlyKeys(keys); len(humanOnly) > 0 {
		writeAgentSettingsRefused(w, r, humanOnly)
		return false
	}
	if agent, ok := scopedAgent(r); ok && agent.DevMode {
		return true // Dev mode skips the agentSettings policy, never the human-only keys above
	}
	decider, ok := c.Policy.(PolicyDecider)
	if !ok || decider == nil {
		writeAgentSettingsRefused(w, r, map[string]string{keys[0]: "agent setting changes need the agent policy, which this daemon lacks"})
		return false
	}
	agent, _ := scopedAgent(r)
	rule := agentsettings.Of(decider.Decide(r.Context(), agentpolicy.SessionRef{Project: project, Kind: domain.KindOrchestrator, ID: agent.SessionID}))
	refused := map[string]string{}
	for _, k := range keys {
		if why := rule.Refusal(k); why != "" {
			refused[k] = why
		}
	}
	if len(refused) == 0 {
		return true
	}
	writeAgentSettingsRefused(w, r, refused)
	return false
}

// humanOnlyKeys names the keys that no agent actor may write, with why.
func humanOnlyKeys(keys []string) map[string]string {
	out := map[string]string{}
	for _, k := range keys {
		def, ok := settingsreg.Default().Lookup(settingsreg.Key(k))
		if ok && !def.CanWrite(settingsreg.ActorOrchestrator) && !def.CanWrite(settingsreg.ActorCreatorOrchestrator) {
			out[k] = "only the user may change it"
		}
	}
	return out
}

func writeAgentSettingsRefused(w http.ResponseWriter, r *http.Request, keys map[string]string) {
	envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "AGENT_NOT_PERMITTED",
		"the agent settings policy refuses this change", map[string]any{"keys": keys})
}

// rulesKeys names the registry keys a rules request writes.
func rulesKeys(in projectsvc.SetRulesInput) []string {
	var keys []string
	for key, set := range map[string]bool{
		"project.agentRules":                         in.AgentRules != nil,
		"project.orchestratorRules":                  in.OrchestratorRules != nil,
		"project.orchestrator.autoCompactWindow":     in.OrchestratorAutoCompactWindow != nil,
		"project.orchestrator.idleCompactAt":         in.OrchestratorIdleCompactAt != nil,
		"project.orchestrator.researchCompactWindow": in.OrchestratorResearchCompactWindow != nil,
	} {
		if set {
			keys = append(keys, key)
		}
	}
	return keys
}

// agentConfigAllowed applies the creator permission cap and the agentSettings
// policy to an agent replacing project id's config; the user always passes.
func (c *ProjectsController) agentConfigAllowed(w http.ResponseWriter, r *http.Request, cfg domain.ProjectConfig) bool {
	agent, ok := scopedAgent(r)
	if !ok {
		return true
	}
	return c.agentPermissionCap(w, r, agent, cfg) &&
		c.agentSettingsAllowed(w, r, projectID(r), c.configKeys(r, projectID(r), cfg))
}

// configKeys names the registry keys a config replacement changes.
func (c *ProjectsController) configKeys(r *http.Request, id domain.ProjectID, next domain.ProjectConfig) []string {
	var before domain.ProjectConfig
	if got, err := c.Mgr.Get(r.Context(), id); err == nil && got.Project != nil && got.Project.Config != nil {
		before = *got.Project.Config
	}
	return changedKeys(before, next)
}

// changedKeys names the registry keys that differ between two configs.
func changedKeys(before, next domain.ProjectConfig) []string {
	changes := settingsaudit.Diff(settingsreg.Default(), "", settingsaudit.ProjectConfig(before), settingsaudit.ProjectConfig(next))
	keys := make([]string, 0, len(changes))
	b, n := configObject(before), configObject(next)
	for _, ch := range changes {
		// A changed object field names each registered sub-key whose own value
		// changed (project.orchestrator.idleCompactAt), so a deny on a nested
		// key holds on the config routes as on PATCH /settings/values. The
		// parent is named only when something outside those sub-keys changed,
		// so an allow list naming the sub-key admits that change.
		var subs [][]string
		for _, def := range settingsreg.Default().All() {
			sub := string(def.Key)
			if !strings.HasPrefix(sub, ch.Key+".") {
				continue
			}
			path := strings.Split(strings.TrimPrefix(sub, "project."), ".")
			subs = append(subs, path)
			if !reflect.DeepEqual(dig(b, path), dig(n, path)) {
				keys = append(keys, sub)
			}
		}
		parent := strings.Split(strings.TrimPrefix(ch.Key, "project."), ".")
		if len(subs) == 0 || !reflect.DeepEqual(without(dig(b, parent), subs, len(parent)), without(dig(n, parent), subs, len(parent))) {
			keys = append(keys, ch.Key)
		}
	}
	return keys
}

// without returns obj, the value at a parent key, with the sub-key paths
// removed (each path starts with the parent's depth names).
func without(obj any, subs [][]string, depth int) any {
	m, ok := obj.(map[string]any)
	if !ok {
		return obj
	}
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	for _, path := range subs {
		rel := path[depth:]
		if len(rel) == 1 {
			delete(out, rel[0])
		}
	}
	return out
}

// configObject is cfg as a generic JSON object.
func configObject(cfg domain.ProjectConfig) map[string]any {
	raw, _ := json.Marshal(cfg)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

// dig returns the value at path in a JSON object, nil when absent.
func dig(obj map[string]any, path []string) any {
	var cur any = obj
	for _, name := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[name]
	}
	return cur
}
