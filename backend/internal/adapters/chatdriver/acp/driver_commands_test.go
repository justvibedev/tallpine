package acp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestACPDriverExposesDynamicAvailableCommandsAsSkills(t *testing.T) {
	agent := &fakeAgent{}
	driver := newFakeDriver(agent)

	conv := startConversation(t, driver)
	lister := conv.(ports.ChatSkillLister)

	if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(conv.ProviderConversationID()),
		Update: acpsdk.SessionUpdate{AvailableCommandsUpdate: &acpsdk.SessionAvailableCommandsUpdate{
			SessionUpdate: "available_commands_update",
			AvailableCommands: []acpsdk.AvailableCommand{{
				Name: "review", Description: "Review a pull request",
				Input: &acpsdk.AvailableCommandInput{Unstructured: &acpsdk.UnstructuredCommandInput{Hint: "<number>"}},
			}},
		}},
	}); err != nil {
		t.Fatalf("SessionUpdate: %v", err)
	}

	skills := awaitSkillCount(t, lister, 1)
	if len(skills) != 1 || skills[0] != (ports.ChatSkill{
		Name: "review", DisplayName: "review", Description: "Review a pull request",
		InputHint: "<number>", Source: "agent",
	}) {
		t.Fatalf("skills = %#v", skills)
	}
	if !conv.Capabilities()[ports.ChatCapabilitySkills] {
		t.Fatal("skills capability was not advertised after the command catalog arrived")
	}

	// ACP updates are snapshots. An empty update removes commands that are no
	// longer available but keeps the feature known, so the UI can render no menu.
	if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(conv.ProviderConversationID()),
		Update: acpsdk.SessionUpdate{AvailableCommandsUpdate: &acpsdk.SessionAvailableCommandsUpdate{
			SessionUpdate:     "available_commands_update",
			AvailableCommands: []acpsdk.AvailableCommand{},
		}},
	}); err != nil {
		t.Fatalf("empty SessionUpdate: %v", err)
	}
	skills = awaitSkillCount(t, lister, 0)
	if len(skills) != 0 {
		t.Fatalf("skills after replacement = %#v, want empty", skills)
	}
}

func TestACPDriverMapsAdvertisedSteeringOntoTallpine(t *testing.T) {
	agent := &fakeAgent{
		steering: true,
		capabilities: &acpsdk.AgentCapabilities{
			PromptCapabilities: acpsdk.PromptCapabilities{Image: true},
			SessionCapabilities: acpsdk.SessionCapabilities{
				Resume: &acpsdk.SessionResumeCapabilities{},
			},
		},
	}
	driver := newFakeDriver(agent)

	opened := startConversation(t, driver)
	if !opened.Capabilities()[ports.ChatCapabilitySteer] {
		t.Fatal("steer capability was not derived from ACP initialize metadata")
	}
	conv := opened.(*conversation)
	conv.mu.Lock()
	conv.activeTurn = "turn-1"
	conv.mu.Unlock()

	ref, err := conv.Steer(context.Background(), "turn-1", ports.ChatUserMessage{
		Text: "focus on the API",
		Content: []ports.ChatContent{{
			Type: "image", Data: "aGVsbG8=", MIMEType: "image/png",
		}},
	})
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if ref.ProviderTurnID != "turn-1" {
		t.Fatalf("steered turn = %q, want turn-1", ref.ProviderTurnID)
	}
	agent.mu.Lock()
	text, meta, prompt := agent.steerText, agent.steerMeta, agent.steerPrompt
	agent.mu.Unlock()
	// B491: a steer aborts the running tool call, which the model sees as an
	// owner denial; the notice must lead the text and name that denial.
	if text != steerCancelNotice+"focus on the API" || !strings.Contains(text, "The user doesn't want to take this action right now") {
		t.Fatalf("steer text = %q", text)
	}
	if len(prompt) != 2 || prompt[1].Image == nil || prompt[1].Image.MimeType != "image/png" {
		t.Fatalf("steer prompt = %#v, want text and image", prompt)
	}
	steering, _ := meta["steering"].(map[string]any)
	if steering["idleBehavior"] != "promptRequired" {
		t.Fatalf("steering meta = %#v", meta)
	}

	agent.mu.Lock()
	agent.steerOut = "promptRequired"
	agent.mu.Unlock()
	if _, err := conv.Steer(context.Background(), "turn-1", ports.ChatUserMessage{Text: "too late"}); !errors.Is(err, ports.ErrChatNoSteerableTurn) {
		t.Fatalf("late steer error = %v, want ErrChatNoSteerableTurn", err)
	}
	if _, err := conv.Steer(context.Background(), "other-turn", ports.ChatUserMessage{Text: "wrong turn"}); !errors.Is(err, ports.ErrChatNoSteerableTurn) {
		t.Fatalf("wrong-turn steer error = %v, want ErrChatNoSteerableTurn", err)
	}
}

func TestACPSteerMethodNotFoundIsDefiniteRefusal(t *testing.T) {
	agent := &fakeAgent{steering: true, steerMethodNotFound: true}
	driver := newFakeDriver(agent)

	conv := activateTurn(startConversation(t, driver))

	_, err := conv.Steer(context.Background(), "turn-1", ports.ChatUserMessage{Text: "focus on the API"})
	if err == nil {
		t.Fatal("Steer: want method-not-found refusal")
	}
	var refusal interface{ ChatRefusal() bool }
	if !errors.As(err, &refusal) || !refusal.ChatRefusal() {
		t.Fatalf("Steer error = %v, want ChatRefusal", err)
	}
	if !isACPMethodNotFound(err) {
		t.Fatalf("Steer error = %v, want wrapped JSON-RPC -32601", err)
	}
}

func TestACPSteerTransportFailureIsNotARefusal(t *testing.T) {
	agent := &fakeAgent{
		steering:          true,
		steerExtensionErr: &acpsdk.RequestError{Code: -32603, Message: "Internal error"},
	}
	driver := newFakeDriver(agent)

	conv := activateTurn(startConversation(t, driver))

	_, err := conv.Steer(context.Background(), "turn-1", ports.ChatUserMessage{Text: "focus on the API"})
	if err == nil {
		t.Fatal("Steer: want transport-style failure")
	}
	var refusal interface{ ChatRefusal() bool }
	if errors.As(err, &refusal) && refusal.ChatRefusal() {
		t.Fatalf("Steer error = %v, must not be ChatRefusal", err)
	}
	if isACPMethodNotFound(err) {
		t.Fatalf("Steer error = %v, must not look like method-not-found", err)
	}
}

// TestACPDriverStartToleratesMethodNotFound verifies that Start() succeeds
// even when the agent returns -32601 for session/set_mode and
// session/set_config_option. The launch-time flags (model, --auto, --yolo)
// are expected to have already applied the initial settings.
func TestACPDriverStartToleratesMethodNotFound(t *testing.T) {
	agent := &fakeAgent{
		modeNotFound:   true,
		configNotFound: true,
	}
	driver := newSetterDriver(agent)

	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(),
		Model:         "glm-5.2",
		Permissions:   ports.PermissionModeAcceptEdits,
	})
	if err != nil {
		t.Fatalf("Start with -32601 setters: %v", err)
	}
	defer conv.Close()
}

// TestACPDriverSendTurnPropagatesMethodNotFound verifies that SendTurn returns
// an actionable error (not a silent skip) when the agent returns -32601 for
// session/set_mode or session/set_config_option during a runtime settings change.
func TestACPDriverSendTurnPropagatesMethodNotFound(t *testing.T) {
	agent := &fakeAgent{
		modeNotFound:   true,
		configNotFound: true,
	}
	driver := newSetterDriver(agent)

	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conv.Close()

	// A runtime mode change should fail with ErrACPSetterUnsupported, not be
	// silently swallowed.
	_, err = conv.SendTurn(context.Background(), ports.ChatUserMessage{
		Text:     "hello",
		Settings: ports.ChatTurnSettings{Approval: ports.PermissionModeAcceptEdits},
	})
	if !errors.Is(err, ErrACPSetterUnsupported) {
		t.Fatalf("SendTurn with -32601 mode setter: err = %v, want ErrACPSetterUnsupported", err)
	}
}

// newSetterDriver builds a driver that applies the session mode and the model
// option through the agent's setters.
func newSetterDriver(agent *fakeAgent) *Driver {
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SessionMode:  func(agentpolicy.Decision, ports.PermissionMode) string { return "acceptEdits" },
		SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
			if settings.Model == "" {
				return nil
			}
			return []SessionOption{{ID: "model", Value: settings.Model}}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	return driver
}
