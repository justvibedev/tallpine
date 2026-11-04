package claudecode

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func hasExplicitDefaultMode(argv []string) bool {
	for i, a := range argv {
		if a == "--permission-mode" && i+1 < len(argv) && argv[i+1] == "default" {
			return true
		}
	}
	return false
}

func TestCappedClaudeDefaultLaunchAndRestoreAreExplicit(t *testing.T) {
	p := &Plugin{resolvedBinary: "claude"}
	launch := func(d agentpolicy.Decision) []string {
		cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{Policy: d, Permissions: ports.PermissionModeDefault, Prompt: "x"})
		if err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	restore := func(d agentpolicy.Decision) []string {
		cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
			Policy:      d,
			Permissions: ports.PermissionModeDefault,
			Session:     ports.SessionRef{ID: "s", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "claude-native-1"}},
		})
		if err != nil || !ok {
			t.Fatalf("restore ok=%v err=%v", ok, err)
		}
		return cmd
	}
	none := agentpolicy.Decision{}
	if hasExplicitDefaultMode(launch(none)) || hasExplicitDefaultMode(restore(none)) {
		t.Fatal("without a cap Claude default must stay implicit (user's settings decide)")
	}
	for _, level := range []permissioncap.Level{permissioncap.Auto, permissioncap.AcceptEdits} {
		d := policytest.Cap(t, level)
		if !hasExplicitDefaultMode(launch(d)) {
			t.Errorf("cap %s: launch lacks --permission-mode default: %v", level, launch(d))
		}
		if !hasExplicitDefaultMode(restore(d)) {
			t.Errorf("cap %s: restore lacks --permission-mode default: %v", level, restore(d))
		}
	}
}
