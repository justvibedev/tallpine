package deepseekharnessacp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/deepseekharness"
	acpdriver "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/acp"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestConfigureBootsACPProfileWithSessionLogUploadOff(t *testing.T) {
	launch, err := launchFor(deepseekharness.New(), "/bin/dsh", nil, t.TempDir(), ports.PermissionModeDefault, agentpolicy.Decision{})
	if err != nil {
		t.Fatal(err)
	}
	args := launch.Args
	if len(args) != 4 || args[0] != "--profile" || args[1] != "acp" || args[2] != "--patch" {
		t.Fatalf("args = %v", args)
	}
	data, err := os.ReadFile(args[3])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "id: session-log-deepseek") || !strings.Contains(string(data), "enabled: false") {
		t.Fatalf("overlay does not disable upload:\n%s", data)
	}
}

func TestEffortMapping(t *testing.T) {
	cases := map[string]string{
		"none": "off", "minimal": "off", "off": "off", "low": "low",
		"medium": "high", "high": "high", "xhigh": "max", "max": "max", "HIGH": "high",
		"turbo": "turbo", // unknown passes through so the agent rejects it
	}
	for in, want := range cases {
		opts := sessionOptions(ports.ChatTurnSettings{Effort: in})
		if len(opts) != 1 || opts[0].ID != "reasoning_effort" || opts[0].Value != want {
			t.Fatalf("effort %q -> %#v, want %q", in, opts, want)
		}
	}
	if sessionOptions(ports.ChatTurnSettings{}) != nil {
		t.Fatal("unset settings produced options")
	}
}

func TestModelIsOpaqueAndValidateRejectsUnmappableEffort(t *testing.T) {
	model := `["deepseek-official","deepseek-flash"]`
	opts := sessionOptions(ports.ChatTurnSettings{Model: model})
	if len(opts) != 1 || opts[0].ID != "model" || opts[0].Value != model {
		t.Fatalf("model option = %#v", opts)
	}
	if err := validateTurnSettings(ports.PermissionModeDefault, ports.ChatTurnSettings{Model: model, Effort: "medium"}); err != nil {
		t.Fatal(err)
	}
	if err := validateTurnSettings(ports.PermissionModeDefault, ports.ChatTurnSettings{Effort: "turbo"}); err == nil {
		t.Fatal("unmappable effort accepted")
	}
}

func TestSessionPreambleDeliversRolePromptOnChat(t *testing.T) {
	blocks := SessionPreamble(acpdriver.LaunchConfig{SystemPrompt: "follow Tallpine rules"})
	if len(blocks) != 1 || blocks[0].Text == nil {
		t.Fatalf("blocks = %#v, want one text block", blocks)
	}
	text := blocks[0].Text.Text
	if !strings.HasPrefix(text, "Standing instructions from Tallpine.") || !strings.Contains(text, "follow Tallpine rules") {
		t.Fatalf("preamble = %q", text)
	}
	if SessionPreamble(acpdriver.LaunchConfig{DisableTools: true}) != nil {
		t.Fatal("empty prompt with tools disabled produced a preamble")
	}
}

func TestSessionPreambleCarriesReadBeforeEditGuidance(t *testing.T) {
	for _, prompt := range []string{"", "follow Tallpine rules"} {
		blocks := SessionPreamble(acpdriver.LaunchConfig{SystemPrompt: prompt})
		if len(blocks) != 1 || blocks[0].Text == nil || !strings.Contains(blocks[0].Text.Text, deepseekGuidance) {
			t.Fatalf("prompt %q: guidance missing: %#v", prompt, blocks)
		}
	}
	if !strings.Contains(deepseekGuidance, "tallpine commit -m") {
		t.Fatalf("guidance does not point a sandboxed worker at tallpine commit: %q", deepseekGuidance)
	}
	if !strings.Contains(deepseekGuidance, "read tool") || !strings.Contains(deepseekGuidance, "sed, cat or grep") {
		t.Fatalf("guidance = %q", deepseekGuidance)
	}
}

func TestPermissionPolicyBypassAllowsEscalationWithoutPrompt(t *testing.T) {
	req := acpsdk.RequestPermissionRequest{
		ToolCall: acpsdk.ToolCallUpdate{},
		Options: []acpsdk.PermissionOption{
			{OptionId: "reject", Kind: acpsdk.PermissionOptionKindRejectOnce},
			{OptionId: "allow", Kind: acpsdk.PermissionOptionKindAllowOnce},
		},
	}
	if id, ok := permissionPolicy(agentpolicy.Decision{}, ports.PermissionModeBypassPermissions, req); !ok || id != "allow" {
		t.Fatalf("bypass escalation = %q %v, want allow", id, ok)
	}
	for _, mode := range []ports.PermissionMode{ports.PermissionModeDefault, ports.PermissionModeAcceptEdits} {
		if _, ok := permissionPolicy(agentpolicy.Decision{}, mode, req); ok {
			t.Fatalf("%s answered a non-edit escalation", mode)
		}
	}
}

func TestLaunchPermissionModeEnv(t *testing.T) {
	launch := func(mode ports.PermissionMode) map[string]string {
		l, err := launchFor(stubPlugin{}, "/bin/dsh", nil, t.TempDir(), mode, agentpolicy.Decision{})
		if err != nil {
			t.Fatal(err)
		}
		return l.Env
	}
	if got := launch(ports.PermissionModeBypassPermissions)["DSH_PERMISSION_MODE"]; got != "danger-full-access" {
		t.Fatalf("bypass DSH_PERMISSION_MODE = %q", got)
	}
	for _, mode := range []ports.PermissionMode{ports.PermissionModeDefault, ports.PermissionModeAcceptEdits, ports.PermissionModeAuto} {
		if got, ok := launch(mode)["DSH_PERMISSION_MODE"]; ok {
			t.Fatalf("%s set DSH_PERMISSION_MODE=%q; sandbox must stay", mode, got)
		}
	}
}

func TestPermissionPolicyAnswersOnceForAutoAndEdits(t *testing.T) {
	edit := acpsdk.ToolKindEdit
	req := func(kind *acpsdk.ToolKind) acpsdk.RequestPermissionRequest {
		return acpsdk.RequestPermissionRequest{
			ToolCall: acpsdk.ToolCallUpdate{Kind: kind},
			Options: []acpsdk.PermissionOption{
				{OptionId: "reject", Kind: acpsdk.PermissionOptionKindRejectOnce},
				{OptionId: "allow", Kind: acpsdk.PermissionOptionKindAllowOnce},
			},
		}
	}
	if id, ok := permissionPolicy(agentpolicy.Decision{}, ports.PermissionModeAuto, req(nil)); !ok || id != "allow" {
		t.Fatalf("auto = %q %v", id, ok)
	}
	if id, ok := permissionPolicy(agentpolicy.Decision{}, ports.PermissionModeAcceptEdits, req(&edit)); !ok || id != "allow" {
		t.Fatalf("accept-edits/edit = %q %v", id, ok)
	}
	if _, ok := permissionPolicy(agentpolicy.Decision{}, ports.PermissionModeAcceptEdits, req(nil)); ok {
		t.Fatal("accept-edits answered a non-edit tool")
	}
	if _, ok := permissionPolicy(agentpolicy.Decision{}, ports.PermissionModeDefault, req(&edit)); ok {
		t.Fatal("default mode answered")
	}
}

func TestDiscoveryLaunchKeepsPrivacyDefaults(t *testing.T) {
	in := map[string]string{"DSH_HOME": "/h"}
	launch, err := DiscoveryLaunch("/bin/dsh", in, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if launch.Command != "/bin/dsh" || len(launch.Args) != 4 || launch.Args[2] != "--patch" {
		t.Fatalf("launch = %#v", launch)
	}
	if launch.Env["DSH_TELEMETRY_MODE"] != "DISABLED" || launch.Env["DSH_HOME"] != "/h" {
		t.Fatalf("env = %v", launch.Env)
	}
	if _, leaked := in["DSH_TELEMETRY_MODE"]; leaked {
		t.Fatal("the caller's env map was mutated")
	}
}

type stubPlugin struct{}

func (stubPlugin) ResolveBinary(context.Context) (string, error) { return "/bin/dsh", nil }
func (stubPlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusAuthorized, nil
}
func (stubPlugin) AugmentRuntimeEnv(env map[string]string, _ string) {
	env["DSH_TELEMETRY_MODE"] = "DISABLED"
}

// The chat launch layers the session env (which carries the private TMPDIR),
// the plugin's runtime policy and the privacy overlay, and fails closed without
// a data dir.
func TestChatLaunchLayersSessionEnvPluginPolicyAndOverlay(t *testing.T) {
	cfg := buildConfig(stubPlugin{}, nil)
	launch, err := cfg.Launch(context.Background(), acpdriver.LaunchConfig{Env: map[string]string{"TMPDIR": "/priv"}, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if launch.Command != "/bin/dsh" || launch.Env["TMPDIR"] != "/priv" || launch.Env["DSH_TELEMETRY_MODE"] != "DISABLED" || launch.Args[2] != "--patch" {
		t.Fatalf("launch = %#v", launch)
	}
	if _, err := cfg.Launch(context.Background(), acpdriver.LaunchConfig{}); err == nil {
		t.Fatal("chat launch without a data dir succeeded")
	}
}

func TestLeavingBypassIsRejectedButEnteringIsNot(t *testing.T) {
	bypass, def := ports.PermissionModeBypassPermissions, ports.PermissionModeDefault
	for _, to := range []ports.PermissionMode{def, ports.PermissionModeAcceptEdits} {
		// A live switch and recovery of a process launched in bypass both reach
		// the validator as (initial=bypass, current=to).
		err := validateTurnSettings(bypass, ports.ChatTurnSettings{Approval: to})
		if !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
			t.Fatalf("bypass->%s err = %v, want ErrChatConfigOptionInvalid", to, err)
		}
	}
	if err := validateTurnSettings(bypass, ports.ChatTurnSettings{Approval: bypass}); err != nil {
		t.Fatalf("bypass->bypass: %v", err)
	}
	if err := validateTurnSettings(def, ports.ChatTurnSettings{Approval: bypass}); err != nil {
		t.Fatalf("default->bypass: %v", err)
	}
	// default->bypass relies on the safety net: no prompt.
	req := acpsdk.RequestPermissionRequest{Options: []acpsdk.PermissionOption{{OptionId: "allow", Kind: acpsdk.PermissionOptionKindAllowOnce}}}
	if id, ok := permissionPolicy(agentpolicy.Decision{}, bypass, req); !ok || id != "allow" {
		t.Fatalf("after entering bypass = %q %v", id, ok)
	}
}

func TestChatDeclaresUsageAndSessionUsageReadsDshLog(t *testing.T) {
	cfg := buildConfig(stubPlugin{}, nil)
	if !cfg.Capabilities[ports.ChatCapabilityUsage] || cfg.SessionUsage == nil {
		t.Fatalf("usage capability/hook missing: %v", cfg.Capabilities)
	}
	// The session's DSH_HOME comes from the launch env, not the daemon's.
	home := t.TempDir()
	writeLogAt(t, filepath.Join(home, "sessions"), []string{record(t, 1, "deepseek-flash", time.Date(2026, 10, 26, 5, 0, 0, 0, time.UTC), 10, 20, 5)})
	if cfg.SessionUsage(testSessionID) != nil {
		t.Fatal("found a log before any launch named its home")
	}
	if _, err := cfg.Launch(context.Background(), acpdriver.LaunchConfig{Env: map[string]string{"DSH_HOME": home}, DataDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	u := cfg.SessionUsage(testSessionID)
	if u == nil || u.InputTokens != 10 || u.CachedTokens != 20 || u.OutputTokens != 5 || u.Cost == nil {
		t.Fatalf("usage: %+v", u)
	}
}
