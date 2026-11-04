package claudecode

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/hooksjson"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// Tallpine's Claude hooks ride the --settings launch flag, not the workspace
// settings.local.json; assert on the exact JSON Claude receives.
func TestClaudeSessionStartHookIncludesResumeSources(t *testing.T) {
	args := claudeLaunchSettingsArgs(domain.AgentLaunchOptions{}, "", nil, agentpolicy.Decision{})
	if len(args) != 2 || args[0] != "--settings" {
		t.Fatalf("unexpected settings args: %v", args)
	}
	var settings struct {
		Hooks map[string][]hooksjson.MatcherGroup `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(args[1]), &settings); err != nil {
		t.Fatalf("decode Claude --settings: %v", err)
	}

	const command = "tallpine hooks claude-code session-start"
	var matcher string
	for _, group := range settings.Hooks["SessionStart"] {
		for _, hook := range group.Hooks {
			if hook.Command == command && group.Matcher != nil {
				matcher = *group.Matcher
			}
		}
	}
	sources := strings.FieldsFunc(matcher, func(r rune) bool { return r == '|' || r == ',' })
	for index := range sources {
		sources[index] = strings.TrimSpace(sources[index])
	}
	for _, source := range []string{"startup", "resume", "clear", "compact", "fork"} {
		if !slices.Contains(sources, source) {
			t.Fatalf("SessionStart matcher %q does not include %q", matcher, source)
		}
	}
}
