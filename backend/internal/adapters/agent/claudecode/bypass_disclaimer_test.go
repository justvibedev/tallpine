package claudecode

import (
	"encoding/json"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/pkg/agentruntime"
)

// B437: Claude Code blocks startup on its "Bypass Permissions mode" dialog
// unless a settings layer records consent. A bypass launch or restore carries
// that consent in Tallpine's flag settings; other modes do not.
func TestBypassLaunchAcceptsTheBypassDisclaimer(t *testing.T) {
	for _, tc := range []struct {
		policy agentruntime.PermissionPolicy
		want   bool
	}{
		{agentruntime.PermissionBypassPermissions, true},
		{agentruntime.PermissionAcceptEdits, false},
		{agentruntime.PermissionAuto, false},
		{"", false},
	} {
		args := claudeProviderArgs(domain.AgentLaunchOptions{}, tc.policy, nil, agentpolicy.Decision{}, false)
		var settings map[string]any
		if len(args) < 2 || args[0] != "--settings" || json.Unmarshal([]byte(args[1]), &settings) != nil {
			t.Fatalf("policy %q: args %#v: want a --settings JSON flag first", tc.policy, args)
		}
		if got := settings["skipDangerousModePermissionPrompt"] == true; got != tc.want {
			t.Fatalf("policy %q: skipDangerousModePermissionPrompt=%v, want %v", tc.policy, got, tc.want)
		}
	}
}
