package kiro

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// TestV3HookPayloadFixtures checks real kiro-cli 2.37.0 (--v3) hook stdin
// payloads captured live: every managed trigger has a fixture whose
// hook_event_name matches and which carries session_id and cwd.
func TestV3HookPayloadFixtures(t *testing.T) {
	for _, spec := range kiroManagedHooks {
		data, err := os.ReadFile(filepath.Join("testdata", "v3hooks", spec.Event+".json"))
		if err != nil {
			t.Fatalf("%s: %v", spec.Event, err)
		}
		var p struct {
			Event     string `json:"hook_event_name"`
			SessionID string `json:"session_id"`
			Cwd       string `json:"cwd"`
		}
		if err := json.Unmarshal(data, &p); err != nil {
			t.Fatal(err)
		}
		if p.Event != spec.Event || p.SessionID == "" || p.Cwd == "" {
			t.Fatalf("%s fixture = %+v", spec.Event, p)
		}
		state, ok := DeriveActivityState(strings.TrimPrefix(spec.Command, kiroHookCommandPrefix), data)
		want := domain.ActivityActive
		if spec.Event == "Stop" || spec.Event == "SessionEnd" {
			want = domain.ActivityIdle
		}
		if !ok || state != want {
			t.Fatalf("%s state = (%v,%v), want %v", spec.Event, state, ok, want)
		}
	}
}

// Kiro v3 splits a compound shell command and checks each part on its own, so
// the `cd <dir> &&` prefix workers use needs its own allow rule in auto mode.
func TestKiroAutoModeAllowsChainedCdCommands(t *testing.T) {
	var shell []string
	for _, r := range kiroPermissionRules(ports.PermissionModeAuto) {
		if r.Capability == "shell" {
			shell = r.Match
		}
	}
	found := false
	for _, m := range shell {
		found = found || m == "cd *"
	}
	if !found {
		t.Fatalf("auto shell rules lack \"cd *\": %v", shell)
	}
	for _, r := range kiroPermissionRules(ports.PermissionModeBypassPermissions) {
		if r.Capability != "all" || r.Effect != "allow" {
			t.Fatalf("bypass rule = %+v", r)
		}
	}
}
