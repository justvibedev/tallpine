package kiro

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/authprobe"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestManifestIDIsKiro(t *testing.T) {
	m := (&Plugin{}).Manifest()
	if m.ID != "kiro" {
		t.Fatalf("manifest ID = %q, want %q", m.ID, "kiro")
	}
	if m.Name != "Kiro" {
		t.Fatalf("manifest Name = %q, want %q", m.Name, "Kiro")
	}
	if len(m.Capabilities) != 1 || m.Capabilities[0] != adapters.CapabilityAgent {
		t.Fatalf("manifest Capabilities = %#v, want [CapabilityAgent]", m.Capabilities)
	}
}

func TestGetLaunchCommandBuildsInteractiveArgv(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Permissions: ports.PermissionModeBypassPermissions,
		Kind:        domain.KindWorker,
		Prompt:      "-fix this",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"kiro-cli", "chat",
		"--v3", "--trust-workspace",
		"--agent", "tallpine",
		"--trust-all-tools",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
	}
}

func TestGetLaunchCommandOrchestratorUsesInteractiveAgent(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Kind:         domain.KindOrchestrator,
		SystemPrompt: "You are the human-facing coordinator.",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"kiro-cli", "chat",
		"--v3", "--trust-workspace",
		"--agent", "tallpine",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
	}
}

func TestGetLaunchCommandPromptlessWorkerStaysInteractive(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"kiro-cli", "chat",
		"--v3", "--trust-workspace",
		"--agent", "tallpine",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
	}
}

func TestGetLaunchCommandPromptedWorkerKeepsPromptOutOfArgv(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Kind:         domain.KindWorker,
		Prompt:       "fix the failing test",
		SystemPrompt: "standing role instructions",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"kiro-cli", "chat",
		"--v3", "--trust-workspace",
		"--agent", "tallpine",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
	}
}

func TestGetLaunchCommandPromptedOrchestratorCarriesPrompt(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Kind:   domain.KindOrchestrator,
		Prompt: "do the explicit task",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"kiro-cli", "chat",
		"--v3", "--trust-workspace",
		"--agent", "tallpine",
		"--", "do the explicit task",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
	}
}

func TestGetLaunchCommandSelectsPreparedCustomAgentForSystemPrompt(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	workspace := t.TempDir()

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Permissions:      ports.PermissionModeBypassPermissions,
		Prompt:           "-fix this",
		SystemPrompt:     "follow Tallpine rules",
		SystemPromptFile: filepath.Join(t.TempDir(), "system.md"),
		WorkspacePath:    workspace,
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"kiro-cli", "chat",
		"--v3", "--trust-workspace",
		"--agent", "tallpine",
		"--trust-all-tools",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
	}
	if _, err := os.Stat(kiroAgentPath(workspace)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("launch wrote agent config, err = %v", err)
	}
}

func TestGetLaunchCommandDoesNotRewritePreparedAgentConfig(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	workspace := t.TempDir()
	promptFile := kiroPromptFile(t, "standing Tallpine instructions")

	if err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		Config:           ports.AgentConfig{Model: "project-model"},
		DataDir:          t.TempDir(),
		SessionID:        "sess-1",
		SystemPrompt:     "standing Tallpine instructions",
		SystemPromptFile: promptFile,
		WorkspacePath:    workspace,
	}); err != nil {
		t.Fatal(err)
	}

	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		SystemPromptFile: promptFile,
		WorkspacePath:    workspace,
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"kiro-cli", "chat",
		"--v3", "--trust-workspace",
		"--agent", "tallpine",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
	}
	var topLevel map[string]json.RawMessage
	data, err := os.ReadFile(kiroAgentPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &topLevel); err != nil {
		t.Fatal(err)
	}
	if got := string(topLevel["prompt"]); got != `"standing Tallpine instructions"` {
		t.Fatalf("agent prompt = %q, want inlined text", got)
	}
	var model string
	if err := json.Unmarshal(topLevel["model"], &model); err != nil {
		t.Fatalf("decode model from %s: %v", data, err)
	}
	if model != "project-model" {
		t.Fatalf("model = %q, want preserved model", model)
	}
}

func TestGetLaunchCommandMapsApprovalModes(t *testing.T) {
	tests := []struct {
		name        string
		permission  ports.PermissionMode
		want        []string
		notExpected []string
	}{
		{
			name:        "default",
			permission:  ports.PermissionModeDefault,
			notExpected: []string{"--trust-all-tools", "--trust-tools=fs_read,fs_write"},
		},
		{
			name:        "accept-edits",
			permission:  ports.PermissionModeAcceptEdits,
			notExpected: []string{"--trust-all-tools", "--trust-tools=fs_read,fs_write"},
		},
		{
			name:        "auto",
			permission:  ports.PermissionModeAuto,
			notExpected: []string{"--trust-all-tools"},
		},
		{
			name:       "bypass-permissions",
			permission: ports.PermissionModeBypassPermissions,
			want:       []string{"--trust-all-tools"},
		},
		{
			name:        "empty",
			permission:  "",
			notExpected: []string{"--trust-all-tools", "--trust-tools=fs_read,fs_write"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := &Plugin{resolvedBinary: "kiro-cli"}
			cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
				Permissions: tt.permission,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(tt.want) > 0 && !containsSubsequence(cmd, tt.want) {
				t.Fatalf("command %#v does not contain %#v", cmd, tt.want)
			}
			for _, missing := range tt.notExpected {
				if contains(cmd, missing) {
					t.Fatalf("command %#v contains %q", cmd, missing)
				}
			}
		})
	}
}

func TestGetLaunchCommandCtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	plugin := &Plugin{}
	if _, err := plugin.GetLaunchCommand(ctx, ports.LaunchConfig{}); err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

func TestGetPromptDeliveryStrategyIsInCommand(t *testing.T) {
	plugin := &Plugin{}

	got, err := plugin.GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.PromptDeliveryInCommand {
		t.Fatalf("unexpected strategy: %q", got)
	}
}

func TestGetPromptDeliveryStrategyPromptedWorkerIsAfterStart(t *testing.T) {
	plugin := &Plugin{}

	got, err := plugin.GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{
		Kind:   domain.KindWorker,
		Prompt: "do this task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.PromptDeliveryAfterStart {
		t.Fatalf("unexpected strategy: %q", got)
	}
}

func TestGetPromptDeliveryStrategyOrchestratorUsesCustomAgent(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}

	got, err := plugin.GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{Kind: domain.KindOrchestrator})
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.PromptDeliveryCustomAgent {
		t.Fatalf("unexpected strategy: %q", got)
	}
}

func TestGetPromptDeliveryStrategyPromptedOrchestratorUsesCommand(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}

	got, err := plugin.GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{
		Kind:   domain.KindOrchestrator,
		Prompt: "do this explicit task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != ports.PromptDeliveryInCommand {
		t.Fatalf("unexpected strategy: %q", got)
	}
}

func TestPromptReadinessHints(t *testing.T) {
	hints, err := (&Plugin{}).PromptReadinessHints(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if hints.Timeout <= 0 || len(hints.Patterns) == 0 {
		t.Fatalf("hints = %#v, want bounded readiness patterns", hints)
	}
}

func TestAuthStatusUsesKiroWhoami(t *testing.T) {
	restore := stubKiroAuthRunner(t, func(_ context.Context, name string, arg ...string) ([]byte, error) {
		if name != "kiro-cli" {
			t.Fatalf("binary = %q, want kiro-cli", name)
		}
		if !reflect.DeepEqual(arg, []string{"whoami", "--format", "json"}) {
			t.Fatalf("args = %#v, want [whoami --format json]", arg)
		}
		return []byte("Logged in with Google\nEmail: user@example.com\n"), nil
	})
	defer restore()

	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	status, err := plugin.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %q, want %q", status, ports.AgentAuthStatusAuthorized)
	}
}

func TestGetConfigSpecReportsModelField(t *testing.T) {
	p := New()

	spec, err := p.GetConfigSpec(context.Background())
	if err != nil {
		t.Fatalf("GetConfigSpec: %v", err)
	}

	var found bool
	for _, f := range spec.Fields {
		if f.Key != "model" {
			continue
		}
		found = true
		if f.Type != ports.ConfigFieldString {
			t.Errorf("model field Type = %v, want %v", f.Type, ports.ConfigFieldString)
		}
		if f.Description == "" {
			t.Error("model field Description is empty")
		}
	}
	if !found {
		t.Fatal("GetConfigSpec did not report a \"model\" field")
	}
}

func TestGetConfigSpecHonorsContextCancellation(t *testing.T) {
	p := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.GetConfigSpec(ctx)
	if err == nil {
		t.Fatal("GetConfigSpec with canceled context: want error, got nil")
	}
}
func TestAuthStatusUnauthorizedFromKiroWhoami(t *testing.T) {
	restore := stubKiroAuthRunner(t, func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("Not logged in\n"), nil
	})
	defer restore()

	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	status, err := plugin.AuthStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("status = %q, want %q", status, ports.AgentAuthStatusUnauthorized)
	}
}

func TestGetAgentHooksInstallsKiroHooks(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	workspace := t.TempDir()
	promptFile := kiroPromptFile(t, "standing Tallpine instructions")
	hooksDir := filepath.Join(workspace, kiroHooksDirName, kiroAgentsDirName)
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(hooksDir, kiroAgentFileName)
	existing := `{"name":"stale","hooks":[{"name":"mine","trigger":"Stop","action":{"type":"command","command":"custom stop hook"}}]}`
	if err := os.WriteFile(hooksPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := ports.WorkspaceHookConfig{
		DataDir:          t.TempDir(),
		SessionID:        "sess-1",
		SystemPrompt:     "standing Tallpine instructions",
		SystemPromptFile: promptFile,
		WorkspacePath:    workspace,
	}
	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	// A second install must not duplicate Tallpine hook commands.
	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(data, &topLevel); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := json.Unmarshal(topLevel["name"], &name); err != nil {
		t.Fatalf("decode name from %s: %v", data, err)
	}
	if name != kiroAgentName {
		t.Fatalf("name = %q, want %q", name, kiroAgentName)
	}
	var prompt string
	if err := json.Unmarshal(topLevel["prompt"], &prompt); err != nil {
		t.Fatalf("decode prompt from %s: %v", data, err)
	}
	// v3 rejects absolute file:// prompt URIs, so the text is inlined.
	if prompt != "standing Tallpine instructions" {
		t.Fatalf("prompt = %q, want inlined system prompt", prompt)
	}

	var config kiroHookFile
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	for _, spec := range kiroManagedHooks {
		if count := countKiroHookCommand(config.Hooks, spec.Command); count != 0 {
			t.Fatalf("%s managed hook leaked into agent profile (v3 ignores it): %#v", spec.Event, config.Hooks)
		}
	}
	// Regression B243: v3 only loads standalone .kiro/hooks/*.json.
	sdata, err := os.ReadFile(kiroStandaloneHooksPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	var standalone struct {
		Version string          `json:"version"`
		Hooks   []kiroHookEntry `json:"hooks"`
	}
	if err := json.Unmarshal(sdata, &standalone); err != nil {
		t.Fatal(err)
	}
	if standalone.Version != "v1" {
		t.Fatalf("standalone version = %q, want v1", standalone.Version)
	}
	for _, spec := range kiroManagedHooks {
		if count := countKiroHookCommand(standalone.Hooks, spec.Command); count != 1 {
			t.Fatalf("%s command count = %d, want 1 in %#v", spec.Event, count, standalone.Hooks)
		}
		for _, h := range standalone.Hooks {
			if h.Action.Command == spec.Command && (h.Trigger != spec.Event || h.Action.Type != "command" || !strings.HasPrefix(h.Name, kiroHookNamePrefix)) {
				t.Fatalf("bad entry shape: %#v", h)
			}
		}
	}
	if countKiroHookCommand(config.Hooks, "custom stop hook") != 1 {
		t.Fatalf("existing stop hook was not preserved: %#v", config.Hooks)
	}
}

func TestGetAgentHooksCreatesNamedKiroAgentFile(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	workspace := t.TempDir()
	hooksPath := kiroAgentPath(workspace)
	promptFile := kiroPromptFile(t, "exact orchestrator system prompt")

	cfg := ports.WorkspaceHookConfig{
		DataDir:          t.TempDir(),
		SessionID:        "sess-1",
		SystemPrompt:     "exact orchestrator system prompt",
		SystemPromptFile: promptFile,
		WorkspacePath:    workspace,
	}
	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(data, &topLevel); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := json.Unmarshal(topLevel["name"], &name); err != nil {
		t.Fatalf("decode name from %s: %v", data, err)
	}
	if name != kiroAgentName {
		t.Fatalf("name = %q, want %q", name, kiroAgentName)
	}
	var config kiroHookFile
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.Prompt == nil || *config.Prompt != "exact orchestrator system prompt" {
		t.Fatalf("prompt = %#v, want inlined system prompt", config.Prompt)
	}
}

func TestGetAgentHooksRequiresSystemPromptFile(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		SessionID:     "sess-1",
		SystemPrompt:  "standing Tallpine instructions",
		WorkspacePath: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for system prompt without prompt file")
	}
	if !strings.Contains(err.Error(), "system prompt file required") {
		t.Fatalf("err = %v, want system prompt file required", err)
	}
}

func TestGetAgentHooksWritesConfiguredModel(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	workspace := t.TempDir()
	hooksPath := kiroAgentPath(workspace)
	promptFile := kiroPromptFile(t, "standing Tallpine instructions")

	cfg := ports.WorkspaceHookConfig{
		Config:           ports.AgentConfig{Model: "claude-sonnet-4-5"},
		DataDir:          t.TempDir(),
		SessionID:        "sess-1",
		SystemPrompt:     "standing Tallpine instructions",
		SystemPromptFile: promptFile,
		WorkspacePath:    workspace,
	}
	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(data, &topLevel); err != nil {
		t.Fatal(err)
	}
	var model string
	if err := json.Unmarshal(topLevel["model"], &model); err != nil {
		t.Fatalf("decode model from %s: %v", data, err)
	}
	if model != "claude-sonnet-4-5" {
		t.Fatalf("model = %q, want configured model", model)
	}
}

func TestGetAgentHooksOverwritesStaleConfiguredModel(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	workspace := t.TempDir()
	hooksPath := kiroAgentPath(workspace)
	promptFile := kiroPromptFile(t, "standing Tallpine instructions")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"name":"tallpine","model":"stale-model","tools":["custom"]}`
	if err := os.WriteFile(hooksPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := ports.WorkspaceHookConfig{
		Config:           ports.AgentConfig{Model: "project-model"},
		DataDir:          t.TempDir(),
		SessionID:        "sess-1",
		SystemPrompt:     "standing Tallpine instructions",
		SystemPromptFile: promptFile,
		WorkspacePath:    workspace,
	}
	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(data, &topLevel); err != nil {
		t.Fatal(err)
	}
	var model string
	if err := json.Unmarshal(topLevel["model"], &model); err != nil {
		t.Fatalf("decode model from %s: %v", data, err)
	}
	if model != "project-model" {
		t.Fatalf("model = %q, want project override", model)
	}
	var tools []string
	if err := json.Unmarshal(topLevel["tools"], &tools); err != nil {
		t.Fatalf("decode tools from %s: %v", data, err)
	}
	if !reflect.DeepEqual(tools, []string{"custom"}) {
		t.Fatalf("tools = %#v, want preserved custom tools", tools)
	}
}

func TestGetAgentHooksClearsStaleModelWhenConfigRemoved(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	workspace := t.TempDir()
	hooksPath := kiroAgentPath(workspace)
	promptFile := kiroPromptFile(t, "standing Tallpine instructions")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"name":"tallpine","model":"stale-model","tools":["custom"]}`
	if err := os.WriteFile(hooksPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := ports.WorkspaceHookConfig{
		DataDir:          t.TempDir(),
		SessionID:        "sess-1",
		SystemPrompt:     "standing Tallpine instructions",
		SystemPromptFile: promptFile,
		WorkspacePath:    workspace,
	}
	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(data, &topLevel); err != nil {
		t.Fatal(err)
	}
	if _, ok := topLevel["model"]; ok {
		t.Fatalf("model still present in %s, want cleared when config has no model", data)
	}
	var tools []string
	if err := json.Unmarshal(topLevel["tools"], &tools); err != nil {
		t.Fatalf("decode tools from %s: %v", data, err)
	}
	if !reflect.DeepEqual(tools, []string{"custom"}) {
		t.Fatalf("tools = %#v, want preserved custom tools", tools)
	}
}

func TestUninstallHooksRemovesKiroHooks(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	workspace := t.TempDir()
	hooksPath := kiroAgentPath(workspace)

	ctx := context.Background()
	cfg := ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "sess-1", WorkspacePath: workspace}

	// Pre-seed a user's own stop hook; it must survive uninstall.
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"hooks":[{"name":"mine","trigger":"Stop","action":{"type":"command","command":"custom stop hook"}}]}`
	if err := os.WriteFile(hooksPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := plugin.GetAgentHooks(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if installed, err := plugin.AreHooksInstalled(ctx, workspace); err != nil || !installed {
		t.Fatalf("AreHooksInstalled after install = (%v, %v), want (true, nil)", installed, err)
	}

	if err := plugin.UninstallHooks(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	if installed, err := plugin.AreHooksInstalled(ctx, workspace); err != nil || installed {
		t.Fatalf("AreHooksInstalled after uninstall = (%v, %v), want (false, nil)", installed, err)
	}

	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var config kiroHookFile
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	for _, spec := range kiroManagedHooks {
		if got := countKiroHookCommand(config.Hooks, spec.Command); got != 0 {
			t.Fatalf("%s command %q count = %d after uninstall, want 0", spec.Event, spec.Command, got)
		}
	}
	if countKiroHookCommand(config.Hooks, "custom stop hook") != 1 {
		t.Fatalf("user stop hook not preserved: %#v", config.Hooks)
	}
}

func TestAreHooksInstalledMissingFile(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	workspace := t.TempDir()
	installed, err := plugin.AreHooksInstalled(context.Background(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Fatal("AreHooksInstalled = true for missing file, want false")
	}
}

func TestGetRestoreCommandReadsAgentSessionID(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}

	cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Permissions: ports.PermissionModeAuto,
		Session: ports.SessionRef{
			Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "uuid-123"},
		},
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	want := []string{
		"kiro-cli", "chat",
		"--v3", "--trust-workspace",
		"--agent", "tallpine",
		"--resume-id", "uuid-123",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("restore cmd\nwant: %#v\n got: %#v", want, cmd)
	}
}

func TestGetRestoreCommandReappliesSystemPromptAgent(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	workspace := t.TempDir()
	promptFile := kiroPromptFile(t, "restore Tallpine rules")
	if err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		Config:           ports.AgentConfig{Model: "project-model"},
		DataDir:          t.TempDir(),
		SessionID:        "sess-1",
		SystemPrompt:     "restore Tallpine rules",
		SystemPromptFile: promptFile,
		WorkspacePath:    workspace,
	}); err != nil {
		t.Fatal(err)
	}

	cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		SystemPromptFile: promptFile,
		Session: ports.SessionRef{
			WorkspacePath: workspace,
			Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: "uuid-123"},
		},
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	want := []string{
		"kiro-cli", "chat",
		"--v3", "--trust-workspace",
		"--agent", "tallpine",
		"--resume-id", "uuid-123",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("restore cmd\nwant: %#v\n got: %#v", want, cmd)
	}
	var config map[string]json.RawMessage
	data, err := os.ReadFile(kiroAgentPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if string(config["prompt"]) != `"restore Tallpine rules"` {
		t.Fatalf("agent config = %#v, want restore prompt", config)
	}
	var model string
	if err := json.Unmarshal(config["model"], &model); err != nil {
		t.Fatalf("decode model from %s: %v", data, err)
	}
	if model != "project-model" {
		t.Fatalf("model = %q, want preserved model", model)
	}
}

func TestGetRestoreCommandOrchestratorUsesInteractiveAgent(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}

	cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Kind:        domain.KindOrchestrator,
		Permissions: ports.PermissionModeDefault,
		Session: ports.SessionRef{
			Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "uuid-123"},
		},
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	want := []string{
		"kiro-cli", "chat",
		"--v3", "--trust-workspace",
		"--agent", "tallpine",
		"--resume-id", "uuid-123",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("restore cmd\nwant: %#v\n got: %#v", want, cmd)
	}
}

func TestGetRestoreCommandFalseWithoutAgentSessionID(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}

	cases := []struct {
		name string
		ref  ports.SessionRef
	}{
		{"empty session ref", ports.SessionRef{}},
		{"empty metadata", ports.SessionRef{Metadata: map[string]string{}}},
		{"blank agent session metadata", ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "   "}}},
		{"workspace path only", ports.SessionRef{WorkspacePath: "/some/path"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
				Permissions: ports.PermissionModeAuto,
				Session:     tc.ref,
			})
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if ok {
				t.Fatalf("ok = true, want false")
			}
			if cmd != nil {
				t.Fatalf("cmd = %#v, want nil", cmd)
			}
		})
	}
}

func TestSessionInfoReadsHookMetadata(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}

	info, ok, err := plugin.SessionInfo(context.Background(), ports.SessionRef{
		WorkspacePath: "/some/path",
		Metadata: map[string]string{
			ports.MetadataKeyAgentSessionID: "uuid-123",
			ports.MetadataKeyTitle:          "Fix login redirect",
			ports.MetadataKeySummary:        "Updated the auth callback and tests.",
			"ignored":                       "not returned",
		},
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if info.AgentSessionID != "uuid-123" {
		t.Fatalf("AgentSessionID = %q, want native id", info.AgentSessionID)
	}
	if info.Title != "Fix login redirect" {
		t.Fatalf("Title = %q, want hook title", info.Title)
	}
	if info.Summary != "Updated the auth callback and tests." {
		t.Fatalf("Summary = %q, want hook summary", info.Summary)
	}
	if info.Metadata != nil {
		t.Fatalf("Metadata = %#v, want nil for Kiro", info.Metadata)
	}
}

func TestSessionInfoFalseWhenNoHookMetadata(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}

	info, ok, err := plugin.SessionInfo(context.Background(), ports.SessionRef{
		WorkspacePath: "/some/path",
		Metadata:      map[string]string{},
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if ok {
		t.Fatalf("ok = true, want false")
	}
	if !reflect.DeepEqual(info, ports.SessionInfo{}) {
		t.Fatalf("info = %#v, want zero value", info)
	}
}

func TestResolveKiroBinaryFallback(t *testing.T) {
	// When the binary is not on PATH or any well-known location, the resolver
	// MUST surface ports.ErrAgentBinaryNotFound rather than a silent string
	// fallback that lets a missing CLI launch into an empty tmux pane.
	bin, err := ResolveKiroBinary(context.Background())
	if err != nil {
		if !errors.Is(err, ports.ErrAgentBinaryNotFound) {
			t.Fatalf("err = %v, want ports.ErrAgentBinaryNotFound", err)
		}
		return
	}
	if bin == "" {
		t.Fatal("ResolveKiroBinary returned empty path with no error")
	}
}

func TestResolveKiroBinaryCtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ResolveKiroBinary(ctx); err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func containsSubsequence(values []string, needle []string) bool {
	if len(needle) == 0 {
		return true
	}

	for start := range values {
		if start+len(needle) > len(values) {
			return false
		}
		ok := true
		for offset, want := range needle {
			if values[start+offset] != want {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}

	return false
}

func stubKiroAuthRunner(t *testing.T, runner func(context.Context, string, ...string) ([]byte, error)) func() {
	t.Helper()
	previous := authprobe.CmdRunner
	authprobe.CmdRunner = runner
	return func() { authprobe.CmdRunner = previous }
}

func countKiroHookCommand(entries []kiroHookEntry, command string) int {
	n := 0
	for _, e := range entries {
		if e.Action.Command == command {
			n++
		}
	}
	return n
}

func kiroPromptFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "system.md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLaunchPinsV3WithModelEffort(t *testing.T) {
	p := &Plugin{resolvedBinary: "kiro-cli"}
	cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Config: ports.AgentConfig{Model: "qwen3-coder-next", Effort: "high"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"kiro-cli", "chat", "--v3", "--trust-workspace", "--agent", "tallpine", "--model", "qwen3-coder-next", "--effort", "high"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("argv = %v, want %v", cmd, want)
	}
}

func TestKiroHooksFlatArrayAndMigrateLegacyObject(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	ws := t.TempDir()
	path := kiroAgentPath(ws)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"hooks":{"stop":[{"command":"tallpine hooks kiro stop"},{"command":"mine","timeout_ms":5000}],"agentSpawn":[{"command":"tallpine hooks kiro session-start"}]}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: ws}); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(path)
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(top["hooks"])), "[") {
		t.Fatalf("hooks not an array: %s", top["hooks"])
	}
	var cfg kiroHookFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hooks) != 1 {
		t.Fatalf("hooks = %#v", cfg.Hooks)
	}
	for _, h := range cfg.Hooks {
		if h.Action.Command == "mine" && h.Trigger != "Stop" {
			t.Errorf("user hook trigger = %q, want Stop", h.Trigger)
		}
	}
	if !strings.Contains(string(data), `"timeout_ms": 5000`) {
		t.Errorf("legacy timeout_ms dropped:\n%s", data)
	}
	for _, h := range cfg.Hooks {
		_ = h
	}
}

func TestKiroAgentPermissionRulesAndSteering(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	type rule struct {
		Capability string   `json:"capability"`
		Match      []string `json:"match"`
		Effect     string   `json:"effect"`
	}
	allows := func(rules []rule, capability string) bool {
		for _, r := range rules {
			if (r.Capability == capability || r.Capability == "all") && r.Effect == "allow" {
				return true
			}
		}
		return false
	}
	cases := []struct {
		mode                    ports.PermissionMode
		read, write, shell, all bool
	}{
		{ports.PermissionModeDefault, true, false, false, false},
		{ports.PermissionModeAcceptEdits, true, true, false, false},
		{ports.PermissionModeAuto, true, true, true, false},
		{ports.PermissionModeBypassPermissions, true, true, true, true},
	}
	for _, c := range cases {
		ws := t.TempDir()
		// Stale legacy fields must be dropped.
		if err := os.MkdirAll(filepath.Dir(kiroAgentPath(ws)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(kiroAgentPath(ws), []byte(`{"allowedTools":["x"],"toolsSettings":{"a":1}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
			WorkspacePath: ws, Config: ports.AgentConfig{Permissions: c.mode},
		}); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(kiroAgentPath(ws))
		var top struct {
			Permissions struct {
				Rules []rule `json:"rules"`
			} `json:"permissions"`
			Resources []string                   `json:"resources"`
			Raw       map[string]json.RawMessage `json:"-"`
		}
		if err := json.Unmarshal(data, &top); err != nil {
			t.Fatal(err)
		}
		if len(top.Permissions.Rules) == 0 {
			t.Fatalf("%s: no permissions rules in %s", c.mode, data)
		}
		r := top.Permissions.Rules
		if allows(r, "fs_read") != c.read || allows(r, "fs_write") != c.write || allows(r, "shell") != c.shell {
			t.Errorf("%s: rules = %#v", c.mode, r)
		}
		if got := len(r) == 1 && r[0].Capability == "all"; got != c.all {
			t.Errorf("%s: allow-all = %v", c.mode, got)
		}
		if strings.Contains(string(data), "allowedTools") || strings.Contains(string(data), "toolsSettings") {
			t.Errorf("%s: legacy fields kept: %s", c.mode, data)
		}
		for _, r := range r {
			for _, m := range r.Match {
				if m == "git *" || m == "gh *" || m == "tallpine *" || m == "tp *" || m == "go *" {
					t.Errorf("%s: bare wildcard grant %q", c.mode, m)
				}
				if strings.HasPrefix(m, "python") || strings.HasPrefix(m, "npx") || m == "npm *" || m == "go *" || m == "make *" {
					t.Errorf("%s: interpreter/runner grant %q", c.mode, m)
				}
				if m == "./**" || m == "cp *" || m == "mv *" || m == "cat *" {
					t.Errorf("%s: undocumented/unscoped match %q", c.mode, m)
				}
			}
			if (r.Capability == "fs_read" || r.Capability == "fs_write") && (len(r.Match) != 1 || r.Match[0] != "**") {
				t.Errorf("%s: fs match = %v, want [**]", c.mode, r.Match)
			}
		}
		if len(top.Resources) != 2 || top.Resources[0] != "file://AGENTS.md" {
			t.Errorf("%s: resources = %v", c.mode, top.Resources)
		}
	}
}
