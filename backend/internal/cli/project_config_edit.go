package cli

import (
	"errors"
	"fmt"
	"strings"
)

// setConfigString sets a string field, removing it when value is empty.
func setConfigString(cfg map[string]any, key, value string) {
	if value == "" {
		delete(cfg, key)
		return
	}
	cfg[key] = value
}

// nestedConfig returns the object stored at key, creating it when absent.
func nestedConfig(cfg map[string]any, key string) map[string]any {
	if m, ok := cfg[key].(map[string]any); ok {
		return m
	}
	m := map[string]any{}
	cfg[key] = m
	return m
}

func pruneEmptyConfig(cfg map[string]any, key string) {
	if m, ok := cfg[key].(map[string]any); ok && len(m) == 0 {
		delete(cfg, key)
	}
}

func deleteNestedConfig(cfg map[string]any, key, field string) {
	if m, ok := cfg[key].(map[string]any); ok {
		delete(m, field)
	}
	pruneEmptyConfig(cfg, key)
}

func setNestedConfigString(cfg map[string]any, key, field, value string) {
	if value == "" {
		deleteNestedConfig(cfg, key, field)
		return
	}
	nestedConfig(cfg, key)[field] = value
}

// setConfigList replaces a string list, removing it when no non-empty value is given.
func setConfigList(cfg map[string]any, key string, values []string) {
	list := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			list = append(list, v)
		}
	}
	if len(list) == 0 {
		delete(cfg, key)
		return
	}
	cfg[key] = list
}

// setConfigReviewers replaces the reviewer list. A harness that is already
// configured keeps its agentConfig.
func setConfigReviewers(cfg map[string]any, harnesses []string) {
	existing := map[string]any{}
	if list, ok := cfg["reviewers"].([]any); ok {
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				if h, _ := m["harness"].(string); h != "" {
					existing[h] = m
				}
			}
		}
	}
	reviewers := make([]any, 0, len(harnesses))
	for _, r := range reviewersForFlags(harnesses) {
		if prev, ok := existing[r.Harness]; ok {
			reviewers = append(reviewers, prev)
			continue
		}
		reviewers = append(reviewers, map[string]any{"harness": r.Harness})
	}
	if len(reviewers) == 0 {
		delete(cfg, "reviewers")
		return
	}
	cfg["reviewers"] = reviewers
}

// reviewersForFlags turns repeated --reviewer harness values into the config
// shape. The daemon validates the harness vocabulary.
func reviewersForFlags(harnesses []string) []reviewerConfig {
	if len(harnesses) == 0 {
		return nil
	}
	reviewers := make([]reviewerConfig, 0, len(harnesses))
	for _, harness := range harnesses {
		harness = strings.TrimSpace(harness)
		if harness == "" {
			continue
		}
		reviewers = append(reviewers, reviewerConfig{Harness: harness})
	}
	if len(reviewers) == 0 {
		return nil
	}
	return reviewers
}

// parseEnvKeys validates the repeated --unset-env KEY flags.
func parseEnvKeys(keys []string) ([]string, error) {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" || strings.Contains(key, "=") {
			return nil, usageError{fmt.Errorf("invalid --unset-env %q: expected a bare KEY", key)}
		}
		out = append(out, key)
	}
	return out, nil
}

// parseEnvPairs turns repeated KEY=VALUE flags into a map.
func parseEnvPairs(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	env := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, usageError{fmt.Errorf("invalid --env %q: expected KEY=VALUE", pair)}
		}
		env[key] = value
	}
	return env, nil
}

// setRoleEffort sets or clears <role>.agentConfig.effort, pruning emptied
// parents so a clear leaves no stray objects.
func setRoleEffort(cfg map[string]any, role, effort string) {
	if effort == "" {
		if m, ok := cfg[role].(map[string]any); ok {
			setAgentConfigEffort(m, "")
			pruneEmptyConfig(cfg, role)
		}
		return
	}
	setAgentConfigEffort(nestedConfig(cfg, role), effort)
}

// setAgentConfigEffort sets or clears parent.agentConfig.effort.
func setAgentConfigEffort(parent map[string]any, effort string) {
	setNestedConfigString(parent, "agentConfig", "effort", effort)
}

// setReviewerEffort sets or clears agentConfig.effort on every configured
// reviewer. Setting needs at least one reviewer to attach to.
func setReviewerEffort(cfg map[string]any, effort string) error {
	list, _ := cfg["reviewers"].([]any)
	if len(list) == 0 && effort != "" {
		return usageError{errors.New("--reviewer-effort needs a configured reviewer; set --reviewer first")}
	}
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			setAgentConfigEffort(m, effort)
		}
	}
	return nil
}
