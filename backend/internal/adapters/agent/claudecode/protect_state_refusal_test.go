package claudecode

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

// B401: protectState on with no run file used to launch unsandboxed with only a
// log line. Each case is what a launch does for that policy, env and OS.
func TestRequireFence(t *testing.T) {
	withRunFile := map[string]string{brand.Env("RUN_FILE"): "/state/running.json"}
	blank := map[string]string{brand.Env("RUN_FILE"): "  "}
	off := policytest.Decision(t, nil)
	for name, tc := range map[string]struct {
		decision agentpolicy.Decision
		env      map[string]string
		goos     string
		refused  bool
	}{
		"on, no run file":      {protectOn(t), nil, "darwin", true},
		"on, blank run file":   {protectOn(t), blank, "darwin", true},
		"on, run file":         {protectOn(t), withRunFile, "darwin", false},
		"off, no run file":     {off, nil, "darwin", false},
		"linux has no fence":   {protectOn(t), nil, "linux", false},
		"zero decision is off": {agentpolicy.Decision{}, nil, "darwin", false},
	} {
		err := RequireFence(tc.decision, tc.env, tc.goos)
		if got := errors.Is(err, statefence.ErrNoRunFile); got != tc.refused {
			t.Errorf("%s: err = %v, refused want %v", name, err, tc.refused)
		}
	}
}

func requireDarwin(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("the Claude fence is macOS-only")
	}
}

// The refusal must come out of the real launch command, with the typed error
// the API maps to POLICY_REQUIRES_FENCE.
func TestGetLaunchCommandRefusesAnEmptyFence(t *testing.T) {
	requireDarwin(t)
	p := &Plugin{resolvedBinary: "claude"}
	_, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{Policy: protectOn(t), SessionID: "s-1", Env: map[string]string{}})
	if !errors.Is(err, statefence.ErrNoRunFile) || !agentpolicy.IsFenceRequired(err) {
		t.Fatalf("err = %v, want the typed no-run-file refusal", err)
	}
	if _, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Policy: protectOn(t), SessionID: "s-1", Env: map[string]string{brand.Env("RUN_FILE"): "/state/running.json"},
	}); err != nil {
		t.Fatalf("fenced launch: %v", err)
	}
	if _, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{Policy: policytest.Decision(t, nil), SessionID: "s-1"}); err != nil {
		t.Fatalf("protectState off must launch without a run file: %v", err)
	}
}

func TestGetRestoreCommandRefusesAnEmptyFence(t *testing.T) {
	requireDarwin(t)
	p := &Plugin{resolvedBinary: "claude"}
	session := ports.SessionRef{ID: "s-1", Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "claude-native-1"}}
	_, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Policy: protectOn(t), Session: session})
	if ok || !errors.Is(err, statefence.ErrNoRunFile) || !agentpolicy.IsFenceRequired(err) {
		t.Fatalf("(ok=%v, err=%v), want the typed no-run-file refusal", ok, err)
	}
	if _, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{Policy: protectOn(t), Session: session, RunFilePath: "/state/running.json"}); err != nil || !ok {
		t.Fatalf("fenced restore: ok=%v err=%v", ok, err)
	}
}
