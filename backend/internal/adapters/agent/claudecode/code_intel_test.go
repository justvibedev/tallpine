package claudecode

import (
	"slices"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Code intelligence mode adds the grep deny list to --settings, beside the
// rules the launch already carries; without it no deny list is written.
func TestLaunchSettingsCodeIntelDeny(t *testing.T) {
	settings := map[string]any{"permissions": map[string]any{"deny": []string{"Read(//x)"}}}
	addLaunchOptionSettings(settings, domain.AgentLaunchOptions{CodeIntel: true})
	deny := settings["permissions"].(map[string]any)["deny"].([]string)
	for _, want := range append([]string{"Read(//x)"}, domain.CodeIntelDeny...) {
		if !slices.Contains(deny, want) {
			t.Fatalf("deny missing %q: %v", want, deny)
		}
	}
	plain := map[string]any{}
	addLaunchOptionSettings(plain, domain.AgentLaunchOptions{})
	if _, ok := plain["permissions"]; ok {
		t.Fatalf("plain launch got permissions: %v", plain)
	}
}
