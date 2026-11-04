package kiro

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

// The Kiro reviewer runs inside the seatbelt fence under Security mode on
// macOS, writable only in its own prompt dir.
func TestReviewCommandFencedUnderSecurityMode(t *testing.T) {
	if !statefence.Available(runtime.GOOS) {
		t.Skip("host cannot fence")
	}
	r := &Reviewer{resolveBinary: func(context.Context) (string, error) { return "kiro-cli", nil }}
	if !ports.FencesState(r, runtime.GOOS) {
		t.Fatal("kiro reviewer must fence on darwin")
	}
	root := t.TempDir()
	systemPath := filepath.Join(root, "system.md")
	if err := os.WriteFile(systemPath, []byte("role\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(root, "review-tmp")
	inv := ports.ReviewInvocation{Policy: policytest.Hardened(t), TempDir: tmp, SystemPromptFile: systemPath, RunFilePath: filepath.Join(root, "running.json"), TaskPromptRoot: filepath.Join(root, "prompt"), WorkspacePath: filepath.Join(root, "ws")}
	for _, withRoot := range []bool{true, false} {
		if !withRoot {
			inv.TaskPromptRoot = ""
		}
		spec, err := r.ReviewCommand(context.Background(), inv)
		if err != nil {
			t.Fatal(err)
		}
		if spec.Argv[0] != statefence.SandboxExec || spec.Argv[3] != "kiro-cli" {
			t.Fatalf("review (prompt root %v) not fenced: %q", withRoot, spec.Argv)
		}
		if !strings.Contains(spec.Argv[2], `(subpath "`+tmp+`")`) {
			t.Fatal("the reviewer temp dir must be writable")
		}
		if strings.Contains(spec.Argv[2], `(subpath "`+inv.WorkspacePath+`")`) {
			t.Fatal("the worker checkout must stay read-only for the reviewer")
		}
	}
	spec, err := r.ReviewCommand(context.Background(), ports.ReviewInvocation{Policy: policytest.Decision(t, nil)})
	if err != nil || spec.Argv[0] != "kiro-cli" {
		t.Fatalf("policy off must launch unwrapped: %q %v", spec.Argv, err)
	}
}
