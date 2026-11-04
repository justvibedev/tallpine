package httpd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// agentConfigKeys is the allowlist of project config an orchestrator may set on
// a project its project created (agent policy crossProjectOrchestration). A
// nested map lists the keys allowed inside that object; nil allows a plain
// value. Everything else (env, hooks, symlinks, tracker intake, models, ...) is
// the user's.
var agentConfigKeys = map[string]map[string]map[string]bool{
	"defaultBranch":     nil,
	"sessionPrefix":     nil,
	"agentRules":        nil,
	"orchestratorRules": nil,
	"noCI":              nil,
	"planInPlace":       nil,
	"eval":              nil,
	"worker":            roleKeys,
	"orchestrator":      orchestratorRoleKeys,
	"planner":           roleKeys,
	"reviewers":         reviewerKeys,
}

var (
	agentConfigFields = map[string]bool{"model": true, "effort": true, "permissions": true}
	roleKeys          = map[string]map[string]bool{"agent": nil, "agentConfig": agentConfigFields}
	// orchestratorRoleKeys adds the orchestrators' compaction window (B193),
	// which the settings registry lets a creating orchestrator write.
	orchestratorRoleKeys = map[string]map[string]bool{"agent": nil, "agentConfig": agentConfigFields, "autoCompactWindow": nil, "idleCompactAt": nil, "researchCompactWindow": nil}
	reviewerKeys         = map[string]map[string]bool{"harness": nil, "agentConfig": agentConfigFields}
)

// agentConfigViolation returns why raw (a project config object) holds a field
// agents may not set, or "". Empty values (null, "", false, 0, empty object or
// list) count as absent: a config read back from the daemon carries many.
func agentConfigViolation(raw []byte) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "" // the handler reports the malformed body
	}
	var bad []string
	for _, key := range sortedKeys(cfg) {
		if isEmptyValue(cfg[key]) {
			continue
		}
		nested, ok := agentConfigKeys[key]
		if !ok {
			bad = append(bad, key)
			continue
		}
		if nested != nil {
			bad = append(bad, nestedViolations(key, cfg[key], nested)...)
		}
	}
	if len(bad) == 0 {
		return ""
	}
	return fmt.Sprintf("agents may not set project config %s (allowed: defaultBranch, sessionPrefix, agentRules, orchestratorRules, noCI, planInPlace, eval, worker/orchestrator/planner/reviewers agent, model, effort, permissions)", strings.Join(bad, ", "))
}

func nestedViolations(key string, value any, allowed map[string]map[string]bool) []string {
	var items []any
	if list, ok := value.([]any); ok {
		items = list
	} else {
		items = []any{value}
	}
	var bad []string
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for _, k := range sortedKeys(obj) {
			if isEmptyValue(obj[k]) {
				continue
			}
			inner, ok := allowed[k]
			if !ok {
				bad = append(bad, key+"."+k)
				continue
			}
			if inner == nil {
				continue
			}
			sub, _ := obj[k].(map[string]any)
			for _, f := range sortedKeys(sub) {
				if !isEmptyValue(sub[f]) && !agentConfigFields[f] {
					bad = append(bad, key+"."+k+"."+f)
				}
			}
		}
	}
	return bad
}

func isEmptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	case float64:
		return x == 0
	case []any:
		for _, e := range x {
			if !isEmptyValue(e) {
				return false
			}
		}
		return true
	case map[string]any:
		for _, e := range x {
			if !isEmptyValue(e) {
				return false
			}
		}
		return true
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
