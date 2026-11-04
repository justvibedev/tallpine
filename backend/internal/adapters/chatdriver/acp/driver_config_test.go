package acp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestACPDriverUsesProviderPermissionPolicyBeforeParking(t *testing.T) {
	agent := &fakeAgent{promptNoPermission: true}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityApprovals: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		PermissionPolicy: func(_ agentpolicy.Decision, mode ports.PermissionMode, params acpsdk.RequestPermissionRequest) (acpsdk.PermissionOptionId, bool) {
			if mode != ports.PermissionModeBypassPermissions {
				return "", false
			}
			return params.Options[0].OptionId, true
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(), Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer opened.Close()
	ready := nextEvent(t, opened.Events())
	if ready.Kind != ports.ChatEventControllerState {
		t.Fatalf("first event = %#v, want controller state", ready)
	}

	conv := opened.(*conversation)
	if err := conv.applyTurnSettings(context.Background(), ports.ChatTurnSettings{}); err != nil {
		t.Fatalf("apply empty settings: %v", err)
	}
	conv.mu.Lock()
	modeAfterEmpty := conv.permissionMode
	conv.mu.Unlock()
	if modeAfterEmpty != ports.PermissionModeBypassPermissions {
		t.Fatalf("permission mode after empty turn settings = %q, want launch mode %q",
			modeAfterEmpty, ports.PermissionModeBypassPermissions)
	}
	response, err := conv.RequestPermission(context.Background(), acpsdk.RequestPermissionRequest{
		ToolCall: acpsdk.ToolCallUpdate{ToolCallId: "edit-1", Kind: acpsdk.Ptr(acpsdk.ToolKindEdit)},
		Options: []acpsdk.PermissionOption{{
			OptionId: "allow-once", Name: "Allow", Kind: acpsdk.PermissionOptionKindAllowOnce,
		}},
	})
	if err != nil {
		t.Fatalf("RequestPermission: %v", err)
	}
	if response.Outcome.Selected == nil || response.Outcome.Selected.OptionId != "allow-once" {
		t.Fatalf("permission response = %#v", response)
	}
	select {
	case event := <-opened.Events():
		t.Fatalf("automatic policy emitted a parked approval: %#v", event)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestACPDriverKeepsPermissionPolicyWhenLaterTurnSettingFails(t *testing.T) {
	agent := &fakeAgent{}
	driver := New(Config{
		Harness: domain.HarnessClaudeCode,
		Probe:   func(context.Context) error { return nil },
		Launch:  func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
			if settings.Model == "" {
				return nil
			}
			return []SessionOption{{ID: "model", Value: settings.Model}}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	opened := startConversation(t, driver)
	_ = nextEvent(t, opened.Events())

	agent.mu.Lock()
	agent.configErr = errors.New("set model failed")
	agent.mu.Unlock()
	conv := opened.(*conversation)
	err := conv.applyTurnSettings(context.Background(), ports.ChatTurnSettings{
		Approval: ports.PermissionModeAcceptEdits,
		Model:    "cursor-model",
	})
	if err == nil {
		t.Fatal("applyTurnSettings succeeded, want config error")
	}
	conv.mu.Lock()
	mode := conv.permissionMode
	conv.mu.Unlock()
	if mode != ports.PermissionModeDefault {
		t.Fatalf("permission mode after rejected settings = %q, want %q", mode, ports.PermissionModeDefault)
	}
}

func TestACPDriverAppliesModelBeforeModelDependentMode(t *testing.T) {
	agent := &fakeAgent{modeRequiresOption: SessionOption{
		ID: "model", Value: "claude-opus-4-6",
	}}
	driver := New(Config{
		Harness: domain.HarnessClaudeCode,
		Probe:   func(context.Context) error { return nil },
		Launch:  func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SessionMode: func(_ agentpolicy.Decision, permission ports.PermissionMode) string {
			if ports.NormalizePermissionMode(permission) == ports.PermissionModeAuto {
				return "auto"
			}
			return ""
		},
		SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
			return []SessionOption{
				{ID: "model", Value: settings.Model},
				{ID: "effort", Value: settings.Effort},
			}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(),
		Model:         "claude-opus-4-6",
		Effort:        "low",
		Permissions:   ports.PermissionModeAuto,
	})
	if err != nil {
		t.Fatalf("Start with a model-dependent Auto mode: %v", err)
	}
	defer opened.Close()

	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.mode != "auto" {
		t.Fatalf("mode = %q, want auto", agent.mode)
	}
	if agent.options["model"] != "claude-opus-4-6" || agent.options["effort"] != "low" {
		t.Fatalf("options = %v, want selected model and effort", agent.options)
	}
}

func TestACPDriverFallsBackFromModelUnsupportedAutoMode(t *testing.T) {
	agent := &fakeAgent{
		newConfig: []acpsdk.SessionConfigOption{
			selectConfigOption("model", "Model", "model", "sonnet", "sonnet", "haiku"),
			selectConfigOption("mode", "Mode", "mode", "auto", "auto", "default", "acceptEdits"),
		},
		setConfig: []acpsdk.SessionConfigOption{
			selectConfigOption("model", "Model", "model", "haiku", "sonnet", "haiku"),
			selectConfigOption("mode", "Mode", "mode", "default", "default", "acceptEdits"),
		},
	}
	driver := New(Config{
		Harness: domain.HarnessClaudeCode,
		Probe:   func(context.Context) error { return nil },
		Launch:  func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SessionMode: func(_ agentpolicy.Decision, permission ports.PermissionMode) string {
			if ports.NormalizePermissionMode(permission) == ports.PermissionModeAuto {
				return "auto"
			}
			return ""
		},
		SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
			return []SessionOption{{ID: "model", Value: settings.Model}}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(),
		Model:         "haiku",
		Permissions:   ports.PermissionModeAuto,
	})
	if err != nil {
		t.Fatalf("Start with model-unsupported Auto mode: %v", err)
	}
	defer opened.Close()

	agent.mu.Lock()
	mode := agent.mode
	agent.mu.Unlock()
	if mode != "default" {
		t.Fatalf("provider mode = %q, want default fallback", mode)
	}
	conv := opened.(*conversation)
	conv.mu.Lock()
	permissionMode := conv.permissionMode
	conv.mu.Unlock()
	if permissionMode != ports.PermissionModeDefault {
		t.Fatalf("conversation permission mode = %q, want default fallback", permissionMode)
	}
}

func TestACPDriverExposesAndMutatesAdvertisedConfigOptions(t *testing.T) {
	initial := []acpsdk.SessionConfigOption{
		selectConfigOption("model", "Model", "model", "sonnet", "sonnet", "opus"),
		booleanConfigOption("fast", "Fast mode", true),
	}
	agent := &fakeAgent{
		newConfig: initial,
		setConfig: []acpsdk.SessionConfigOption{
			selectConfigOption("model", "Model", "model", "opus", "sonnet", "opus"),
			selectConfigOption("effort", "Effort", "thought_level", "high", "low", "high"),
			booleanConfigOption("fast", "Fast mode", true),
		},
	}
	driver := newFakeDriver(agent)

	conv := startConversation(t, driver)
	configurer := conv.(ports.ChatConfigOptionController)
	if !conv.Capabilities()[ports.ChatCapabilityConfigOptions] {
		t.Fatal("config_options capability was not advertised")
	}

	options, err := configurer.ListConfigOptions(context.Background())
	if err != nil {
		t.Fatalf("ListConfigOptions: %v", err)
	}
	if len(options) != 2 || options[0].Current.Select != "sonnet" {
		t.Fatalf("initial options = %#v", options)
	}
	if options[1].Current.Boolean == nil || !*options[1].Current.Boolean {
		t.Fatalf("boolean option = %#v", options[1])
	}

	if _, err := configurer.SetConfigOption(context.Background(), "model", ports.ChatConfigOptionValue{Select: "unknown"}); !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("invalid selection error = %v", err)
	}
	options, err = configurer.SetConfigOption(context.Background(), "model", ports.ChatConfigOptionValue{Select: "opus"})
	if err != nil {
		t.Fatalf("SetConfigOption: %v", err)
	}
	if len(options) != 3 || options[0].Current.Select != "opus" || options[1].Category != "thought_level" {
		t.Fatalf("replacement options = %#v", options)
	}
	agent.mu.Lock()
	gotValue, calls := agent.options["model"], agent.setCalls
	agent.mu.Unlock()
	if gotValue != "opus" || calls != 1 {
		t.Fatalf("agent received model = %q across %d calls", gotValue, calls)
	}
}

func TestACPDriverConsumesLegacyKimiSelectorsOnSDK0135(t *testing.T) {
	agent := &legacyKimiAgent{}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
			if settings.Model == "" {
				return nil
			}
			return []SessionOption{{ID: "model", Value: settings.Model}}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeLegacyKimiSpawn(agent))

	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(), Model: "kimi-code/kimi-for-coding",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conv.Close()

	options, err := conv.(ports.ChatConfigOptionController).ListConfigOptions(context.Background())
	if err != nil {
		t.Fatalf("ListConfigOptions: %v", err)
	}
	if len(options) != 2 || options[0].ID != "model" || options[0].Current.Select != "kimi-code/kimi-for-coding" ||
		options[1].ID != "mode" || options[1].Current.Select != "default" {
		t.Fatalf("legacy Kimi options = %#v, want model and default mode", options)
	}

	controller := conv.(ports.ChatConfigOptionController)
	if _, err := controller.SetConfigOption(context.Background(), "model", ports.ChatConfigOptionValue{
		Select: "kimi-code/kimi-for-coding",
	}); err != nil {
		t.Fatalf("SetConfigOption model: %v", err)
	}
	if _, err := controller.SetConfigOption(context.Background(), "mode", ports.ChatConfigOptionValue{
		Select: "default",
	}); err != nil {
		t.Fatalf("SetConfigOption mode: %v", err)
	}

	agent.mu.Lock()
	model, modelCalls := agent.model, agent.modelCalls
	mode, modeCalls, configCalls := agent.mode, agent.modeCalls, agent.configCalls
	agent.mu.Unlock()
	// Start finds the session already on the model and sends nothing; the explicit set does.
	if model != "kimi-code/kimi-for-coding" || modelCalls != 1 ||
		mode != "default" || modeCalls != 1 || configCalls != 0 {
		t.Fatalf("legacy setters: model=%q/%d mode=%q/%d config=%d",
			model, modelCalls, mode, modeCalls, configCalls)
	}
}

func TestACPDriverResolvesCLIModelToAdvertisedParameterizedLegacyChoice(t *testing.T) {
	agent := &legacyKimiAgent{
		currentModel: "auto",
		availableModels: []legacyModelInfo{
			{ModelID: "auto", Name: "Auto"},
			{ModelID: "composer-2.5[fast=false]", Name: "Composer 2.5"},
			{ModelID: "composer-2.5[fast=true]", Name: "Composer 2.5 Fast"},
		},
		rejectUnknownModel: true,
	}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
			if settings.Model == "" {
				return nil
			}
			return []SessionOption{{ID: "model", Value: settings.Model}}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeLegacyKimiSpawn(agent))

	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(), Model: "composer-2.5",
	})
	if err != nil {
		t.Fatalf("Start with CLI model alias: %v", err)
	}
	defer conv.Close()

	agent.mu.Lock()
	model, calls := agent.model, agent.modelCalls
	agent.mu.Unlock()
	if model != "composer-2.5[fast=false]" || calls != 1 {
		t.Fatalf("legacy model setter = %q across %d calls, want advertised non-fast value", model, calls)
	}
}

func TestACPDriverRejectsNonFastAliasWhenOnlyFastParameterizedChoiceIsAdvertised(t *testing.T) {
	agent := &legacyKimiAgent{
		currentModel: "auto",
		availableModels: []legacyModelInfo{
			{ModelID: "auto", Name: "Auto"},
			{ModelID: "composer-2.5[fast=true]", Name: "Composer 2.5 Fast"},
		},
		rejectUnknownModel: true,
	}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
			if settings.Model == "" {
				return nil
			}
			return []SessionOption{{ID: "model", Value: settings.Model}}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeLegacyKimiSpawn(agent))

	_, err := driver.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(), Model: "composer-2.5",
	})
	if !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("Start with unavailable non-fast alias: err = %v, want ErrChatConfigOptionInvalid", err)
	}
	agent.mu.Lock()
	calls := agent.modelCalls
	agent.mu.Unlock()
	if calls != 0 {
		t.Fatalf("legacy model setter called %d times, want 0", calls)
	}
}

func TestResolveLegacyModelChoiceDerivesParameterizedCursorAliases(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		choice    string
	}{
		{
			name:      "fast",
			requested: "composer-2.5-fast",
			choice:    "composer-2.5[fast=true]",
		},
		{
			name:      "reasoning",
			requested: "gpt-5.5-medium",
			choice:    "gpt-5.5[context=272k,reasoning=medium,fast=false]",
		},
		{
			name:      "reasoning and fast",
			requested: "gpt-5.5-high-fast",
			choice:    "gpt-5.5[context=272k,reasoning=high,fast=true]",
		},
		{
			name:      "effort",
			requested: "gemini-3.6-flash-high",
			choice:    "gemini-3.6-flash[effort=high]",
		},
		{
			name:      "reasoning effort",
			requested: "gpt-5.5-low",
			choice:    "gpt-5.5[reasoning_effort=low,fast=false]",
		},
		{
			name:      "thinking with effort",
			requested: "claude-opus-5-thinking-high",
			choice:    "claude-opus-5[thinking=true,context=300k,effort=high,fast=false]",
		},
		{
			name:      "thinking after effort",
			requested: "claude-4.6-sonnet-medium-thinking",
			choice:    "claude-4.6-sonnet[thinking=true,context=1m,effort=medium,fast=false]",
		},
		{
			name:      "thinking without effort",
			requested: "claude-4.5-sonnet-thinking",
			choice:    "claude-4.5-sonnet[thinking=true,context=200k]",
		},
		{
			name:      "cursor-prefixed grok",
			requested: "cursor-grok-4.6-high-fast",
			choice:    "grok-4.6[effort=high,fast=true]",
		},
		{
			name:      "auto",
			requested: "auto",
			choice:    "default[]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			choices := []ports.ChatConfigOptionChoice{{Value: tt.choice}}
			got, ok := resolveLegacyModelChoice(choices, tt.requested)
			if !ok || got != tt.choice {
				t.Fatalf("resolveLegacyModelChoice(%q) = %q, %v; want %q, true", tt.requested, got, ok, tt.choice)
			}
		})
	}
}

func TestResolveLegacyModelChoiceRejectsDroppedParameterizedSemantics(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		choice    string
	}{
		{
			name:      "thinking variant is not non-thinking alias",
			requested: "claude-opus-5-high",
			choice:    "claude-opus-5[thinking=true,context=300k,effort=high,fast=false]",
		},
		{
			name:      "thinking without effort has no known alias",
			requested: "claude-opus-5",
			choice:    "claude-opus-5[thinking=true,context=300k,fast=false]",
		},
		{
			name:      "unknown semantic parameter",
			requested: "future-model",
			choice:    "future-model[quality=high,fast=false]",
		},
		{
			name:      "conflicting effort parameters",
			requested: "gpt-5.5-high",
			choice:    "gpt-5.5[reasoning=high,reasoning_effort=medium]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			choices := []ports.ChatConfigOptionChoice{{Value: tt.choice}}
			if got, ok := resolveLegacyModelChoice(choices, tt.requested); ok {
				t.Fatalf("resolveLegacyModelChoice(%q) = %q, true; want rejection", tt.requested, got)
			}
		})
	}
}

func TestResolveLegacyModelChoiceDistinguishesThinkingVariants(t *testing.T) {
	choices := []ports.ChatConfigOptionChoice{
		{Value: "claude-opus-5[thinking=false,context=300k,effort=high,fast=false]"},
		{Value: "claude-opus-5[thinking=true,context=300k,effort=high,fast=false]"},
	}
	tests := []struct {
		requested string
		want      string
	}{
		{
			requested: "claude-opus-5-high",
			want:      choices[0].Value,
		},
		{
			requested: "claude-opus-5-thinking-high",
			want:      choices[1].Value,
		},
	}
	for _, tt := range tests {
		t.Run(tt.requested, func(t *testing.T) {
			got, ok := resolveLegacyModelChoice(choices, tt.requested)
			if !ok || got != tt.want {
				t.Fatalf("resolveLegacyModelChoice(%q) = %q, %v; want %q, true", tt.requested, got, ok, tt.want)
			}
		})
	}
}

func TestACPDriverRejectsLegacyModelAliasWithoutAdvertisedModelCatalog(t *testing.T) {
	agent := &legacyKimiAgent{
		currentModel: "auto",
		availableModels: []legacyModelInfo{
			{ModelID: "auto", Name: "Auto"},
			{ModelID: "composer-2.5[fast=false]", Name: "Composer 2.5"},
		},
		rejectUnknownModel: true,
	}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeLegacyKimiSpawn(agent))

	conv := startConversation(t, driver)

	conv.(*conversation).replaceConfigOptions(nil)
	_, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{
		Text: "hello", Settings: ports.ChatTurnSettings{Model: "composer-2.5"},
	})
	if !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("SendTurn without advertised model catalog: err = %v, want ErrChatConfigOptionInvalid", err)
	}
	agent.mu.Lock()
	calls := agent.modelCalls
	agent.mu.Unlock()
	if calls != 0 {
		t.Fatalf("legacy model setter called %d times, want 0", calls)
	}
}

func TestACPDriverRejectsAmbiguousLegacyModelAlias(t *testing.T) {
	agent := &legacyKimiAgent{
		currentModel: "auto",
		availableModels: []legacyModelInfo{
			{ModelID: "composer-2.5[fast=false]", Name: "Composer 2.5"},
			{ModelID: "composer-2.5[context=1m,fast=false]", Name: "Composer 2.5 1M"},
		},
		rejectUnknownModel: true,
	}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeLegacyKimiSpawn(agent))

	_, err := driver.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(), Model: "composer-2.5",
	})
	if !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("Start with ambiguous model alias: err = %v, want ErrChatConfigOptionInvalid", err)
	}
	agent.mu.Lock()
	calls := agent.modelCalls
	agent.mu.Unlock()
	if calls != 0 {
		t.Fatalf("legacy model setter called %d times, want 0", calls)
	}
}

func TestDiscoverConfigOptionsReadsSessionCatalogWithoutPrompt(t *testing.T) {
	agent := &fakeAgent{newConfig: []acpsdk.SessionConfigOption{
		selectConfigOption("model", "Model", "model", "sonnet", "sonnet", "opus"),
	}}
	driver := New(Config{
		Harness: domain.HarnessClaudeCode,
		Launch: func(context.Context, LaunchConfig) (Launch, error) {
			return Launch{Command: "cline", Args: []string{"--acp"}}, nil
		},
	}, slog.New(slog.DiscardHandler))
	driver.useTestProcess(fakeSpawn(agent))

	got, err := driver.discoverConfigOptions(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Category != "model" || got[0].Current.Select != "sonnet" || len(got[0].Choices) != 2 {
		t.Fatalf("options = %#v", got)
	}
	if agent.promptParams.Prompt != nil {
		t.Fatalf("discovery sent a prompt: %#v", agent.promptParams)
	}
}

func TestACPDriverRejectsUnsupportedTurnSettingsAtStartAndSend(t *testing.T) {
	agent := &fakeAgent{}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		ValidateTurnSettings: func(_ ports.PermissionMode, settings ports.ChatTurnSettings) error {
			if ports.NormalizePermissionMode(settings.Approval) == ports.PermissionModeDefault {
				return nil
			}
			return ports.ErrChatPermissionModeUnsupported
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	_, err := driver.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(), Permissions: ports.PermissionModeAuto,
	})
	if !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
		t.Fatalf("Start unsupported permissions error = %v", err)
	}

	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start default permissions: %v", err)
	}
	defer conv.Close()
	_, err = conv.SendTurn(context.Background(), ports.ChatUserMessage{
		Text: "do work", Settings: ports.ChatTurnSettings{Approval: ports.PermissionModeAuto},
	})
	if !errors.Is(err, ports.ErrChatPermissionModeUnsupported) {
		t.Fatalf("SendTurn unsupported permissions error = %v", err)
	}
}

func TestNormalizeMCPServersAllowsStdioWithoutCapabilities(t *testing.T) {
	for _, serverType := range []string{"", "stdio"} {
		servers, err := normalizeMCPServers([]ports.ChatMCPServerConfig{{
			Name: "test", Type: serverType, Command: "echo",
		}}, acpsdk.McpCapabilities{})
		if err != nil {
			t.Fatalf("normalizeMCPServers(type %q): %v", serverType, err)
		}
		if len(servers) != 1 || servers[0].Stdio == nil {
			t.Fatalf("servers = %#v, want one stdio server", servers)
		}
	}
}

func TestNormalizeMCPServersStillGatesOptionalTransports(t *testing.T) {
	for _, serverType := range []string{"http", "sse"} {
		_, err := normalizeMCPServers([]ports.ChatMCPServerConfig{{
			Name: "test", Type: serverType, URL: "https://example.test",
		}}, acpsdk.McpCapabilities{})
		if err == nil || !strings.Contains(err.Error(), "does not support") {
			t.Fatalf("normalizeMCPServers(type %q) error = %v, want capability error", serverType, err)
		}
	}
}

// TestNormalizeMCPServersEmptyReturnsEmptySlice verifies that empty configs
// return a non-nil empty slice (not nil) so the SDK serializes it correctly.
func TestNormalizeMCPServersEmptyReturnsEmptySlice(t *testing.T) {
	servers, err := normalizeMCPServers(nil, acpsdk.McpCapabilities{})
	if err != nil {
		t.Fatalf("normalizeMCPServers(nil): %v", err)
	}
	if servers == nil {
		t.Fatal("servers = nil, want non-nil empty slice")
	}
	if len(servers) != 0 {
		t.Fatalf("servers = %d, want 0", len(servers))
	}
}

// TestACPDriverPreservesEarlyConfigOptionUpdates verifies that config option
// updates received via session/update during session/new are not overwritten
// when the NewSession response carries an empty config options catalog.
func TestACPDriverPreservesEarlyConfigOptionUpdates(t *testing.T) {
	earlyOption := selectConfigOption("model", "Model", "model", "glm-5.2", "glm-5.2", "kimi")
	agent := &fakeAgent{
		newConfig: nil, // session/new response has no config options
		newSessionUpdates: []acpsdk.SessionUpdate{
			{ConfigOptionUpdate: &acpsdk.SessionConfigOptionUpdate{ConfigOptions: []acpsdk.SessionConfigOption{earlyOption}}},
		},
	}
	driver := newFakeDriver(agent)

	conv := startConversation(t, driver)

	configurer := conv.(ports.ChatConfigOptionController)
	options, err := configurer.ListConfigOptions(context.Background())
	if err != nil {
		t.Fatalf("ListConfigOptions: %v", err)
	}
	if len(options) != 1 {
		t.Fatalf("options = %d, want 1 (early update preserved)", len(options))
	}
	if options[0].ID != "model" {
		t.Fatalf("option id = %q, want %q", options[0].ID, "model")
	}
}

// TestPR5208FreshStartAppliesSelectedEffort verifies that the selected effort
// level is applied through ACP before the first prompt.
func TestPR5208FreshStartAppliesSelectedEffort(t *testing.T) {
	effortOption := selectConfigOption("effort", "Effort", "effort", "default", "default", "low", "high")
	effortOptionLow := selectConfigOption("effort", "Effort", "effort", "low", "default", "low", "high")
	agent := &fakeAgent{
		newConfig: []acpsdk.SessionConfigOption{effortOption},
		setConfig: []acpsdk.SessionConfigOption{effortOptionLow},
	}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
			if settings.Effort == "" {
				return nil
			}
			return []SessionOption{{ID: "effort", Value: settings.Effort}}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(),
		Effort:        "low",
	})
	if err != nil {
		t.Fatalf("Start with effort=low: %v", err)
	}
	defer conv.Close()

	agent.mu.Lock()
	setCalls := agent.setCalls
	effortValue := agent.options["effort"]
	agent.mu.Unlock()
	if setCalls == 0 || effortValue != "low" {
		t.Fatalf("provider setter calls = %d, effort = %q; want at least one call with low", setCalls, effortValue)
	}

	configurer := conv.(ports.ChatConfigOptionController)
	configOptions, err := configurer.ListConfigOptions(context.Background())
	if err != nil {
		t.Fatalf("ListConfigOptions: %v", err)
	}
	for _, option := range configOptions {
		if option.ID == "effort" {
			if option.Current.Select != "low" {
				t.Fatalf("live effort option = %q, want low", option.Current.Select)
			}
			return
		}
	}
	t.Fatal("effort option not found in live config")
}

// Launch-scoped options are set once the session exists, before the launch's
// turn settings, and again after session/load.
func TestACPDriverAppliesLaunchSessionOptionsOnStartAndResume(t *testing.T) {
	catalog := []acpsdk.SessionConfigOption{
		selectConfigOption("agent", "Agent", "", "", "", "tallpine-sess1"),
	}
	agent := &fakeAgent{newConfig: catalog, setConfig: catalog}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true, ports.ChatCapabilityResume: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		LaunchSessionOptions: func(cfg LaunchConfig) []SessionOption {
			return []SessionOption{{ID: "agent", Value: "tallpine-" + string(cfg.SessionID)}}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{
		SessionID: domain.SessionID("sess1"), WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	conv.Close()
	agent.mu.Lock()
	got, calls := agent.options["agent"], agent.setCalls
	agent.options = nil
	agent.mu.Unlock()
	if got != "tallpine-sess1" || calls != 1 {
		t.Fatalf("after Start: agent option = %q in %d set calls, want tallpine-sess1 in 1", got, calls)
	}

	resumed, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: domain.SessionID("sess1"), WorkspacePath: t.TempDir(),
		ProviderConversationID: "claude-session-1",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer resumed.Close()
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.options["agent"] != "tallpine-sess1" {
		t.Errorf("after Resume: agent option = %q, want tallpine-sess1", agent.options["agent"])
	}
}

// A setter may accept a launch option without returning the catalog. Keep the
// remaining advertised choices available for subsequent launch settings.
func TestACPDriverPreservesCatalogAcrossLaunchOptionsWithoutReturnedCatalog(t *testing.T) {
	catalog := []acpsdk.SessionConfigOption{
		selectConfigOption("agent", "Agent", "", "default", "default", "copilot"),
		selectConfigOption("allow_all", "Allow all", "", "off", "on", "off"),
	}
	agent := &fakeAgent{newConfig: catalog}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		LaunchSessionOptions: func(LaunchConfig) []SessionOption {
			return []SessionOption{{ID: "agent", Value: "copilot"}}
		},
		SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
			if settings.Approval == ports.PermissionModeBypassPermissions {
				return []SessionOption{{ID: "allow_all", Value: "on"}}
			}
			return nil
		},
		RequireAdvertisedOptions: true,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{
		SessionID: domain.SessionID("sess1"), WorkspacePath: t.TempDir(),
		Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conv.Close()

	agent.mu.Lock()
	defer agent.mu.Unlock()
	if got := agent.options["agent"]; got != "copilot" {
		t.Errorf("agent option = %q, want copilot", got)
	}
	if got := agent.options["allow_all"]; got != "on" {
		t.Errorf("allow_all option = %q, want on", got)
	}
	if agent.setCalls != 2 {
		t.Errorf("set config calls = %d, want 2", agent.setCalls)
	}
}

// A setter may accept a change without returning the rebuilt catalog.
// SetConfigOption returns nil so callers requiring an authoritative confirmation
// can fail closed, while the in-memory catalog preserves the accepted option.
func TestACPDriverSetConfigOptionReturnsNilWhenAgentOmitsCatalog(t *testing.T) {
	catalog := []acpsdk.SessionConfigOption{
		selectConfigOption("model", "Model", "", "sonnet", "sonnet", "opus"),
	}
	agent := &fakeAgent{newConfig: catalog}
	driver := newFakeDriver(agent)

	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{
		SessionID: domain.SessionID("sess1"), WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conv.Close()

	controller, ok := conv.(ports.ChatConfigOptionController)
	if !ok {
		t.Fatal("conversation does not implement ChatConfigOptionController")
	}

	// fakeAgent has setConfig == nil by default, so SetSessionConfigOption returns empty ConfigOptions.
	updated, err := controller.SetConfigOption(context.Background(), "model", ports.ChatConfigOptionValue{Select: "opus"})
	if err != nil {
		t.Fatalf("SetConfigOption: %v", err)
	}
	if updated != nil {
		t.Fatalf("SetConfigOption returned %v, want nil when provider omits catalog", updated)
	}

	options, err := controller.ListConfigOptions(context.Background())
	if err != nil {
		t.Fatalf("ListConfigOptions: %v", err)
	}
	if len(options) != 1 || options[0].Current.Select != "opus" {
		t.Fatalf("preserved options = %v, want model=opus", options)
	}
}

// RequireAdvertisedOptions makes an option the session never offered fail
// instead of reaching an agent that would acknowledge and drop it.
func TestACPDriverRequireAdvertisedOptionsRejectsUnadvertised(t *testing.T) {
	catalog := []acpsdk.SessionConfigOption{
		selectConfigOption("allow_all", "Allow all", "", "off", "on", "off"),
	}
	newDriver := func(agent *fakeAgent) *Driver {
		driver := New(Config{
			Harness:      domain.HarnessClaudeCode,
			Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
			Probe:        func(context.Context) error { return nil },
			Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
			SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
				var out []SessionOption
				if settings.Model != "" {
					out = append(out, SessionOption{ID: "model", Value: settings.Model})
				}
				if settings.Approval != "" {
					out = append(out, SessionOption{ID: "allow_all", Value: "on"})
				}
				return out
			},
			RequireAdvertisedOptions: true,
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		driver.useTestProcess(fakeSpawn(agent))
		return driver
	}

	agent := &fakeAgent{newConfig: catalog, setConfig: catalog}
	_, err := newDriver(agent).Start(context.Background(), ports.ChatStartConfig{
		SessionID: domain.SessionID("sess1"), WorkspacePath: t.TempDir(), Model: "gpt-x",
	})
	if !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("Start with unadvertised model: err = %v, want ErrChatConfigOptionInvalid", err)
	}
	agent.mu.Lock()
	if _, sent := agent.options["model"]; sent {
		t.Error("unadvertised model option reached the agent")
	}
	agent.mu.Unlock()

	agent = &fakeAgent{newConfig: catalog, setConfig: catalog}
	conv, err := newDriver(agent).Start(context.Background(), ports.ChatStartConfig{
		SessionID: domain.SessionID("sess1"), WorkspacePath: t.TempDir(),
		Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("Start with advertised option: %v", err)
	}
	defer conv.Close()
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.options["allow_all"] != "on" {
		t.Errorf("allow_all = %q, want on", agent.options["allow_all"])
	}
}

// OptionCatalogWait gives an agent that publishes its model catalog after
// session/new answers time to do so before the option is refused.
func TestACPDriverWaitsForLateAdvertisedOption(t *testing.T) {
	early := []acpsdk.SessionConfigOption{selectConfigOption("allow_all", "Allow all", "", "off", "on", "off")}
	late := append(append([]acpsdk.SessionConfigOption(nil), early...), selectConfigOption("model", "Model", "model", "a", "a", "b"))
	start := func(wait time.Duration) error {
		driver := New(Config{
			Harness:      domain.HarnessClaudeCode,
			Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
			Probe:        func(context.Context) error { return nil },
			Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
			SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
				return []SessionOption{{ID: "model", Value: settings.Model}}
			},
			RequireAdvertisedOptions: true,
			OptionCatalogWait:        wait,
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		driver.useTestProcess(fakeSpawn(&fakeAgent{newConfig: early, setConfig: late, lateConfig: late}))
		conv, err := driver.Start(context.Background(), ports.ChatStartConfig{
			SessionID: domain.SessionID("sess1"), WorkspacePath: t.TempDir(), Model: "b",
		})
		if err == nil {
			_ = conv.Close()
		}
		return err
	}
	if err := start(0); !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("no wait: err = %v, want ErrChatConfigOptionInvalid", err)
	}
	if err := start(3 * time.Second); err != nil {
		t.Fatalf("with wait: %v", err)
	}
}

func TestAwaitConfigOptionStopsOnContextCancel(t *testing.T) {
	c := &conversation{optionCatalogWait: time.Minute}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	started := time.Now()
	_, known, err := c.awaitConfigOption(ctx, SessionOption{ID: "model", Value: "a"})
	if !errors.Is(err, context.Canceled) || known {
		t.Fatalf("known=%v err=%v, want context.Canceled", known, err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("wait ignored cancellation for %v", elapsed)
	}
}

func TestAwaitConfigOptionSeesLateCatalog(t *testing.T) {
	c := &conversation{optionCatalogWait: 5 * time.Second, capabilities: ports.ChatCapabilities{}}
	time.AfterFunc(60*time.Millisecond, func() {
		c.replaceConfigOptions([]acpsdk.SessionConfigOption{selectConfigOption("model", "Model", "model", "a", "a", "b")})
	})
	offered, known, err := c.awaitConfigOption(context.Background(), SessionOption{ID: "model", Value: "b"})
	if err != nil || !known || !offered {
		t.Fatalf("offered=%v known=%v err=%v", offered, known, err)
	}
}
