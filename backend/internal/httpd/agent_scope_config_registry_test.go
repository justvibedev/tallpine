package httpd

import (
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// The settings registry declares which project settings a creating
// orchestrator may write; agentConfigKeys enforces it. Neither may drift.
func TestAgentConfigKeysMatchSettingsRegistry(t *testing.T) {
	var declared []string
	for _, s := range settingsreg.Default().Scope(settingsreg.ScopeProject) {
		if s.CanWrite(settingsreg.ActorCreatorOrchestrator) {
			key := strings.TrimPrefix(string(s.Key), "project.")
			// agentConfigKeys allows top-level config keys; a nested setting
			// (orchestrator.autoCompactWindow) is written through its parent,
			// which must itself be declared.
			if parent, child, nested := strings.Cut(key, "."); nested {
				// The nested allowlist must accept it too, or a config that
				// holds it is refused as a whole (agentConfigViolation).
				children, ok := agentConfigKeys[parent]
				if _, allowed := children[child]; !ok || !allowed {
					t.Errorf("%s: creator may write it, but agentConfigKeys[%q] does not allow %q", s.Key, parent, child)
				}
				continue
			}
			declared = append(declared, key)
		}
	}
	var enforced []string
	for k := range agentConfigKeys {
		enforced = append(enforced, k)
	}
	slices.Sort(declared)
	slices.Sort(enforced)
	if !slices.Equal(declared, enforced) {
		t.Fatalf("registry creatorOrchestrator keys %v != agentConfigKeys %v", declared, enforced)
	}
}
