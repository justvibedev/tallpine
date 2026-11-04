package kiroacp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/acp"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type fakePlugin struct {
	binary string
	err    error
	status ports.AgentAuthStatus
}

func (f fakePlugin) ResolveBinary(context.Context) (string, error) { return f.binary, f.err }
func (f fakePlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return f.status, nil
}

func TestLaunchUsesV3ACPWithoutRejectedFlags(t *testing.T) {
	t.Parallel()
	cfg := buildConfig(fakePlugin{binary: "/bin/kiro-cli"}, nil)
	launch, err := cfg.Launch(context.Background(), acpdriver.LaunchConfig{
		Env: map[string]string{"K": "v"}, Permissions: ports.PermissionModeBypassPermissions, Model: "qwen3-coder-next",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"acp", "--agent-engine", "v3", "--auth-method", "cli"}
	if launch.Command != "/bin/kiro-cli" || !slices.Equal(launch.Args, want) {
		t.Fatalf("launch = %s %v, want %v (kiro v3 acp rejects --agent/--model/--effort/--trust-all-tools)", launch.Command, launch.Args, want)
	}
	if launch.Env["K"] != "v" {
		t.Fatalf("env not forwarded: %v", launch.Env)
	}
}

func TestSessionOptionsSelectProfileAndModel(t *testing.T) {
	t.Parallel()
	cfg := buildConfig(fakePlugin{}, nil)
	launch := cfg.LaunchSessionOptions(acpdriver.LaunchConfig{WorkspacePath: profileWorkspace(t)})
	if len(launch) != 1 || launch[0].ID != "mode" || launch[0].Value != "tallpine" {
		t.Fatalf("launch options = %+v, want mode=tallpine", launch)
	}
	turn := cfg.SessionOptions(ports.ChatTurnSettings{Model: " qwen3-coder-next ", Effort: "high"})
	if len(turn) != 1 || turn[0].ID != "model" || turn[0].Value != "qwen3-coder-next" {
		t.Fatalf("turn options = %+v, want only model (no effort option on kiro v3)", turn)
	}
	if got := cfg.SessionOptions(ports.ChatTurnSettings{}); got != nil {
		t.Fatalf("empty settings = %+v", got)
	}
	if !cfg.RequireAdvertisedOptions {
		t.Fatal("a missing profile must fail visibly")
	}
}

func TestProbe(t *testing.T) {
	t.Parallel()
	missing := errors.New("missing")
	if err := buildConfig(fakePlugin{err: missing}, nil).Probe(context.Background()); !errors.Is(err, ports.ErrChatDriverUnavailable) {
		t.Fatalf("missing binary = %v", err)
	}
	if err := buildConfig(fakePlugin{binary: "x", status: ports.AgentAuthStatusUnauthorized}, nil).Probe(context.Background()); !errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatalf("unauthorized = %v", err)
	}
	if err := buildConfig(fakePlugin{binary: "x", status: ports.AgentAuthStatusAuthorized}, nil).Probe(context.Background()); err != nil {
		t.Fatalf("authorized = %v", err)
	}
}

func request(capability string) acpsdk.RequestPermissionRequest {
	return acpsdk.RequestPermissionRequest{
		Meta: map[string]any{"kiro": map[string]any{"consent": map[string]any{"capability": capability}}},
		Options: []acpsdk.PermissionOption{
			{OptionId: "always-accept", Kind: acpsdk.PermissionOptionKindAllowAlways},
			{OptionId: "accept", Kind: acpsdk.PermissionOptionKindAllowOnce},
			{OptionId: "reject", Kind: acpsdk.PermissionOptionKindRejectOnce},
		},
	}
}

func TestPermissionPolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		mode       ports.PermissionMode
		capability string
		want       bool
	}{
		{"default asks for writes", ports.PermissionModeDefault, "fs_write", false},
		{"accept-edits allows writes", ports.PermissionModeAcceptEdits, "fs_write", true},
		{"accept-edits asks for shell", ports.PermissionModeAcceptEdits, "shell", false},
		{"auto allows writes", ports.PermissionModeAuto, "fs_write", true},
		{"auto leaves shell to the profile allow-list", ports.PermissionModeAuto, "shell", false},
		{"bypass allows shell", ports.PermissionModeBypassPermissions, "shell", true},
		{"missing capability is not guessed", ports.PermissionModeAcceptEdits, "", false},
	} {
		id, ok := permissionPolicy(agentpolicy.Decision{}, tc.mode, request(tc.capability))
		if ok != tc.want || (ok && id != "accept") {
			t.Errorf("%s: got (%q, %v), want handled=%v with the one-shot allow", tc.name, id, ok, tc.want)
		}
	}
	if _, ok := permissionPolicy(agentpolicy.Decision{}, ports.PermissionModeBypassPermissions, acpsdk.RequestPermissionRequest{
		Options: []acpsdk.PermissionOption{{OptionId: "always-accept", Kind: acpsdk.PermissionOptionKindAllowAlways}},
	}); ok {
		t.Error("only allow_always offered: must not grant a standing allow")
	}
}

func TestDriverIdentity(t *testing.T) {
	t.Parallel()
	driver := New(fakePlugin{}, nil)
	if driver.Harness() != domain.HarnessKiro {
		t.Fatalf("harness = %s", driver.Harness())
	}
}

func TestValidateTurnSettingsRefusesLoweringPermissions(t *testing.T) {
	t.Parallel()
	cfg := buildConfig(fakePlugin{}, nil)
	for _, tc := range []struct {
		initial, requested ports.PermissionMode
		refused            bool
	}{
		{ports.PermissionModeBypassPermissions, ports.PermissionModeDefault, true},
		{ports.PermissionModeAuto, ports.PermissionModeDefault, true},
		{ports.PermissionModeAcceptEdits, ports.PermissionModeDefault, true},
		{ports.PermissionModeBypassPermissions, ports.PermissionModeAuto, true},
		{ports.PermissionModeAuto, ports.PermissionModeAuto, false},
		{ports.PermissionModeDefault, ports.PermissionModeBypassPermissions, false},
		{ports.PermissionModeAuto, "", false},
	} {
		err := cfg.ValidateTurnSettings(tc.initial, ports.ChatTurnSettings{Approval: tc.requested})
		if tc.refused != errors.Is(err, ports.ErrChatConfigOptionInvalid) {
			t.Errorf("%s -> %s: err = %v, want refused=%v", tc.initial, tc.requested, err, tc.refused)
		}
	}
}

func profileWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	agents := filepath.Join(dir, ".kiro", "agents")
	if err := os.MkdirAll(agents, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agents, "tallpine.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A background task (title and branch naming) runs in a scratch directory with no
// profile; requesting the `tallpine` mode there fails because Kiro does not offer it.
func TestLaunchSessionOptionsSkipModeWithoutProfile(t *testing.T) {
	t.Parallel()
	cfg := buildConfig(fakePlugin{}, nil)
	if got := cfg.LaunchSessionOptions(acpdriver.LaunchConfig{WorkspacePath: t.TempDir()}); got != nil {
		t.Fatalf("scratch workspace requested options %v, want none", got)
	}
	if got := cfg.LaunchSessionOptions(acpdriver.LaunchConfig{WorkspacePath: profileWorkspace(t)}); len(got) != 1 || got[0].ID != "mode" || got[0].Value != "tallpine" {
		t.Fatalf("session workspace options = %v, want mode=tallpine", got)
	}
}

func TestValidateTurnSettingsRefusesLoweringOnly(t *testing.T) {
	lower := validateTurnSettings(ports.PermissionModeAuto, ports.ChatTurnSettings{Approval: ports.PermissionModeAcceptEdits})
	if !errors.Is(lower, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("lowering: err = %v", lower)
	}
	for _, requested := range []ports.PermissionMode{"", ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
		if err := validateTurnSettings(ports.PermissionModeAuto, ports.ChatTurnSettings{Approval: requested}); err != nil {
			t.Fatalf("%q: err = %v", requested, err)
		}
	}
}

func TestTurnUsageSumsCreditsAndCountsOneTurn(t *testing.T) {
	meta := map[string]any{"kiro": map[string]any{"promptTurnSummaries": []any{
		map[string]any{"unit": "credit", "usage": 0.04},
		map[string]any{"unit": "credit", "usage": 0.01},
		map[string]any{"unit": "token", "usage": 99.0},
	}}}
	got := turnUsage(meta, "qwen3-coder-next")
	if len(got) != 1 || got[0].Model != "qwen3-coder-next" || got[0].Turns != 1 || got[0].Credits < 0.0499 || got[0].Credits > 0.0501 {
		t.Fatalf("got %+v", got)
	}
	if got := turnUsage(meta, ""); len(got) != 1 || got[0].Model != "kiro" {
		t.Fatalf("unnamed model: %+v", got)
	}
	if turnUsage(map[string]any{}, "m") != nil {
		t.Fatal("no summaries must yield no usage")
	}
}

func TestInfoContextUsageReadsPercentage(t *testing.T) {
	t.Parallel()
	meta := map[string]any{"kiro": map[string]any{"contextUsage": map[string]any{"usagePercentage": 37.4}}}
	used, window, ok := infoContextUsage(meta)
	if !ok || used != 37 || window != 100 {
		t.Fatalf("got %d/%d ok=%v", used, window, ok)
	}
	if _, _, ok := infoContextUsage(map[string]any{"kiro": map[string]any{}}); ok {
		t.Fatal("missing figure must not report")
	}
}
