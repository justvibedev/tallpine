package kiroacp

import (
	"path/filepath"
	"runtime"
	"testing"

	acpdriver "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/acp"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

// Kiro chat runs inside the seatbelt fence under Security mode on macOS, and
// the chat service admits it under the hardened profile.
func TestKiroChatFencedUnderSecurityMode(t *testing.T) {
	if !statefence.Available(runtime.GOOS) {
		t.Skip("host cannot fence")
	}
	d := New(fakePlugin{binary: "kiro-cli"}, nil)
	if !ports.FencesState(d, runtime.GOOS) || ports.FencesState(d, "linux") {
		t.Fatal("kiro chat must fence on darwin only")
	}
	hardened := policytest.Hardened(t)
	env := map[string]string{brand.Env("RUN_FILE"): filepath.Join(t.TempDir(), "running.json")}
	launch, err := launchFor("kiro-cli", acpdriver.LaunchConfig{Env: env, Policy: hardened, WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if launch.Command != statefence.SandboxExec || launch.Args[0] != "-p" || launch.Args[2] != "kiro-cli" || launch.Args[3] != "acp" {
		t.Fatalf("launch not fenced: %s %q", launch.Command, launch.Args)
	}
	plain, err := launchFor("kiro-cli", acpdriver.LaunchConfig{Env: env, Policy: policytest.Decision(t, nil)})
	if err != nil || plain.Command != "kiro-cli" {
		t.Fatalf("policy off must launch unwrapped: %s %v", plain.Command, err)
	}
}
