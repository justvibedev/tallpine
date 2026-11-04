package acp

import (
	"context"
	"encoding/json"
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

func TestACPDriverDefersPromptUntilDurableTurnBinding(t *testing.T) {
	agent := &fakeAgent{}
	cfg := fakeConfig(ports.ChatCapabilityApprovals, ports.ChatCapabilityInterrupt, ports.ChatCapabilityResume)
	cfg.SessionMeta = func(cfg LaunchConfig) map[string]any {
		return map[string]any{"systemPrompt": map[string]any{"append": cfg.SystemPrompt}, "disableTools": cfg.DisableTools}
	}
	cfg.SessionMode = func(_ agentpolicy.Decision, permission ports.PermissionMode) string {
		if permission == ports.PermissionModeAcceptEdits {
			return "acceptEdits"
		}
		return ""
	}
	cfg.SessionOptions = modelSessionOption
	driver := newDriverWith(cfg, agent)

	conversation, err := driver.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(), SystemPrompt: "Tallpine instructions", DisableTools: true,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conversation.Close()
	if got := conversation.ProviderConversationID(); got != "claude-session-1" {
		t.Fatalf("provider conversation id = %q", got)
	}
	agent.mu.Lock()
	meta := agent.newParams.Meta
	agent.mu.Unlock()
	if meta["systemPrompt"] == nil {
		t.Fatal("session/new did not receive provider metadata")
	}
	if meta["disableTools"] != true {
		t.Fatal("session/new did not receive the background tool restriction")
	}

	// Consume controller.ready from session setup.
	_ = nextEvent(t, conversation.Events())
	ref, err := conversation.SendTurn(context.Background(), ports.ChatUserMessage{
		Text: "change it", Settings: ports.ChatTurnSettings{
			Model: "test-model", Approval: ports.PermissionModeAcceptEdits,
		},
	})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	select {
	case event := <-conversation.Events():
		t.Fatalf("event %q arrived before StartDeferredTurn; it could race durable binding", event.Kind)
	case <-time.After(30 * time.Millisecond):
	}
	agent.mu.Lock()
	mode, model := agent.mode, agent.options["model"]
	agent.mu.Unlock()
	if mode != "acceptEdits" || model != "test-model" {
		t.Fatalf("ACP settings = mode %q, model %q", mode, model)
	}
	deferred := conversation.(ports.ChatDeferredTurnStarter)
	if err := deferred.StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}

	var approvalID string
	for approvalID == "" {
		event := nextEvent(t, conversation.Events())
		if event.ProviderTurnID != "" && event.ProviderTurnID != ref.ProviderTurnID {
			t.Fatalf("event turn id = %q, want %q", event.ProviderTurnID, ref.ProviderTurnID)
		}
		if event.Kind == ports.ChatEventApprovalRequested {
			approvalID = event.RequestID
			if len(event.Decisions) != 2 || event.Decisions[0].ID != "allow" ||
				event.Decisions[0].Kind != ports.ChatDecisionAllowOnce ||
				event.Decisions[1].Kind != ports.ChatDecisionRejectOnce {
				t.Fatalf("approval decisions = %#v", event.Decisions)
			}
			var detail struct {
				SubjectKind string          `json:"subjectKind"`
				ToolKind    acpsdk.ToolKind `json:"toolKind"`
			}
			if err := json.Unmarshal(event.Detail, &detail); err != nil {
				t.Fatalf("approval detail: %v (%s)", err, event.Detail)
			}
			if detail.SubjectKind != string(domain.ActivityKindFileChange) || detail.ToolKind != acpsdk.ToolKindEdit {
				t.Fatalf("approval detail = %+v, want an ACP file edit", detail)
			}
		}
	}
	if err := conversation.ResolveRequest(context.Background(), approvalID, ports.ChatDecision{ID: "allow"}); err != nil {
		t.Fatalf("ResolveRequest: %v", err)
	}

	var completed bool
	for !completed {
		event := nextEvent(t, conversation.Events())
		if event.Kind == ports.ChatEventTurnCompleted {
			completed = true
			if event.TurnState != domain.TurnStateCompleted {
				t.Fatalf("turn state = %q", event.TurnState)
			}
		}
	}
}

func TestACPDriverValidatesHandshakeIdentityBeforeOpeningSession(t *testing.T) {
	agent := &fakeAgent{}
	validated := false
	cfg := fakeConfig(ports.ChatCapabilityApprovals, ports.ChatCapabilityInterrupt, ports.ChatCapabilityResume)
	cfg.ValidateInitialize = func(acpsdk.InitializeResponse) error {
		validated = true
		return errors.New("unsupported adapter version")
	}
	driver := newDriverWith(cfg, agent)

	_, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if !validated {
		t.Fatal("initialize response was not validated")
	}
	if !errors.Is(err, ports.ErrChatDriverIncompatible) || !strings.Contains(err.Error(), "unsupported adapter version") {
		t.Fatalf("Start error = %v", err)
	}
	agent.mu.Lock()
	opened := agent.newParams.Cwd != ""
	agent.mu.Unlock()
	if opened {
		t.Fatal("session/new ran before the handshake identity was admitted")
	}
}

func TestACPDriverRunsAfterInitializeBeforeOpeningSession(t *testing.T) {
	agent := &fakeAgent{}
	cfg := fakeConfig()
	cfg.AfterInitialize = func(ctx context.Context, conn *acpsdk.ClientSideConnection, _ acpsdk.InitializeResponse, launchCfg LaunchConfig) error {
		if launchCfg.Env["GEMINI_HOME"] != "/session/gemini" {
			t.Errorf("AfterInitialize environment = %v", launchCfg.Env)
		}
		agent.mu.Lock()
		opened := agent.newParams.Cwd != ""
		agent.mu.Unlock()
		if opened {
			t.Error("session/new ran before AfterInitialize")
		}
		_, err := conn.Authenticate(ctx, acpsdk.AuthenticateRequest{MethodId: "oauth-personal"})
		return err
	}
	driver := newDriverWith(cfg, agent)

	conversation, err := driver.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(), Env: map[string]string{"GEMINI_HOME": "/session/gemini"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conversation.Close() }()
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.authMethods) != 1 || agent.authMethods[0] != "oauth-personal" {
		t.Fatalf("authenticate calls = %v, want [oauth-personal]", agent.authMethods)
	}
	if agent.newParams.Cwd == "" {
		t.Fatal("session/new did not run after AfterInitialize succeeded")
	}
}

func TestACPDriverAfterInitializeFailureStopsStart(t *testing.T) {
	agent := &fakeAgent{}
	cfg := fakeConfig()
	cfg.AfterInitialize = func(context.Context, *acpsdk.ClientSideConnection, acpsdk.InitializeResponse, LaunchConfig) error {
		return &acpsdk.RequestError{Code: -32000, Message: "Authentication required"}
	}
	driver := newDriverWith(cfg, agent)

	_, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if !errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatalf("Start error = %v, want ErrChatAuthRequired", err)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.newParams.Cwd != "" {
		t.Fatal("session/new ran after AfterInitialize failed")
	}
}

func TestACPInterruptWaitsForProviderPromptCompletion(t *testing.T) {
	cancelReceived := make(chan struct{}, 1)
	promptReceived := make(chan struct{}, 1)
	completePrompt := make(chan struct{})
	responseSent := make(chan error, 1)
	driver := New(Config{
		Harness: domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true,
			ports.ChatCapabilityInterrupt: true,
		},
		Probe: func(context.Context) error { return nil },
		Launch: func(context.Context, LaunchConfig) (Launch, error) {
			return Launch{Command: "fake"}, nil
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(delayedCancelSpawn(promptReceived, cancelReceived, completePrompt, responseSent))

	conversation, err := driver.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conversation.Close()
	_ = nextEvent(t, conversation.Events()) // controller.ready
	ref, err := conversation.SendTurn(context.Background(), ports.ChatUserMessage{Text: "wait"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conversation.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	select {
	case <-promptReceived:
	case <-time.After(time.Second):
		t.Fatal("provider did not receive ACP prompt request")
	}
	if err := conversation.Interrupt(context.Background(), ref.ProviderTurnID); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	select {
	case <-cancelReceived:
	case <-time.After(time.Second):
		t.Fatal("provider did not receive ACP cancel notification")
	}

	// Accepting session/cancel is not a terminal result. Tallpine must remain busy
	// until the provider resolves the original session/prompt request.
	quiet := time.After(100 * time.Millisecond)
	for {
		select {
		case event := <-conversation.Events():
			if event.Kind == ports.ChatEventTurnCompleted {
				t.Fatalf("turn completed before provider prompt returned: %#v", event)
			}
		case <-quiet:
			close(completePrompt)
			if err := <-responseSent; err != nil {
				t.Fatalf("provider prompt response: %v", err)
			}
			goto waitForCompletion
		}
	}

waitForCompletion:
	for {
		event := nextEvent(t, conversation.Events())
		if event.Kind == ports.ChatEventTurnCompleted {
			if event.TurnState != domain.TurnStateInterrupted {
				t.Fatalf("turn state = %q, want interrupted", event.TurnState)
			}
			break
		}
	}
}

func TestACPDriverNegotiatesRichClientCapabilitiesAndNativePromptContent(t *testing.T) {
	agent := &fakeAgent{
		promptNoPermission: true,
		capabilities: &acpsdk.AgentCapabilities{
			PromptCapabilities: acpsdk.PromptCapabilities{Image: true, EmbeddedContext: true},
			McpCapabilities:    acpsdk.McpCapabilities{Http: true},
			SessionCapabilities: acpsdk.SessionCapabilities{
				Resume:                &acpsdk.SessionResumeCapabilities{},
				AdditionalDirectories: &acpsdk.SessionAdditionalDirectoriesCapabilities{},
			},
		},
	}
	driver := newFakeDriver(agent, ports.ChatCapabilityImages)

	root := t.TempDir()
	extra := t.TempDir()
	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath:         root,
		AdditionalDirectories: []string{extra},
		MCPServers: []ports.ChatMCPServerConfig{
			{Name: "local", Type: "stdio", Command: "mcp-local", Args: []string{"serve"}, Env: map[string]string{"TOKEN": "secret"}},
			{Name: "remote", Type: "http", URL: "https://mcp.example.test", Headers: map[string]string{"Authorization": "Bearer secret"}},
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conv.Close()
	_ = nextEvent(t, conv.Events())

	agent.mu.Lock()
	initParams, newParams := agent.initParams, agent.newParams
	agent.mu.Unlock()
	if initParams.ClientCapabilities.Elicitation == nil ||
		initParams.ClientCapabilities.Elicitation.Form == nil ||
		initParams.ClientCapabilities.Elicitation.Url == nil {
		t.Fatalf("elicitation capabilities = %#v", initParams.ClientCapabilities.Elicitation)
	}
	if initParams.ClientCapabilities.Meta["subagent-transcript"] != true ||
		initParams.ClientCapabilities.Meta["terminal_output"] != true {
		t.Fatalf("client extension capabilities = %#v", initParams.ClientCapabilities.Meta)
	}
	if len(newParams.AdditionalDirectories) != 1 || newParams.AdditionalDirectories[0] != extra {
		t.Fatalf("additional directories = %#v", newParams.AdditionalDirectories)
	}
	if len(newParams.McpServers) != 2 || newParams.McpServers[0].Stdio == nil || newParams.McpServers[1].Http == nil {
		t.Fatalf("MCP servers = %#v", newParams.McpServers)
	}
	if !conv.Capabilities().Has(ports.ChatCapabilityImages) ||
		!conv.Capabilities().Has(ports.ChatCapabilityEmbeddedContext) ||
		!conv.Capabilities().Has(ports.ChatCapabilityResourceLinks) ||
		!conv.Capabilities().Has(ports.ChatCapabilityElicitation) {
		t.Fatalf("conversation capabilities = %#v", conv.Capabilities())
	}

	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{
		Text: "inspect these", ClientMessageID: "tallpine-client-message-1",
		Content: []ports.ChatContent{
			{Type: "image", Data: "aW1hZ2U=", MIMEType: "image/png"},
			{Type: "resource_link", URI: "file:///repo/README.md", Name: "README.md"},
			{Type: "resource", URI: "file:///repo/notes.txt", Name: "notes.txt", MIMEType: "text/plain", Text: "notes"},
			{Type: "resource", URI: ports.ChatInternalReplayResourceURI, Name: "replay", MIMEType: "application/json", Text: `{}`, Internal: true},
		},
	})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	for {
		if event := nextEvent(t, conv.Events()); event.Kind == ports.ChatEventTurnCompleted {
			break
		}
	}
	agent.mu.Lock()
	prompt := agent.promptParams.Prompt
	promptMessageID := agent.promptParams.MessageId
	agent.mu.Unlock()
	if len(prompt) != 5 || prompt[1].Image == nil || prompt[2].ResourceLink == nil ||
		prompt[3].Resource == nil || prompt[4].Resource == nil {
		t.Fatalf("native prompt = %#v", prompt)
	}
	internalResource := prompt[4].Resource.Resource.TextResourceContents
	if internalResource == nil || internalResource.Meta[tallpineInternalReplayMetaKey] != true {
		t.Fatalf("internal replay ACP metadata = %#v", internalResource)
	}
	if promptMessageID == nil || *promptMessageID != "tallpine-client-message-1" {
		t.Fatalf("ACP prompt message id = %v, want Tallpine's durable client id", promptMessageID)
	}
}

func TestACPDriverReappliesLaunchContextWhenResuming(t *testing.T) {
	agent := &fakeAgent{}
	var got LaunchConfig
	cfg := fakeConfig()
	cfg.Launch = func(_ context.Context, launchCfg LaunchConfig) (Launch, error) {
		got = launchCfg
		return Launch{Command: "fake"}, nil
	}
	cfg.SessionMeta = func(cfg LaunchConfig) map[string]any {
		return map[string]any{"systemPrompt": map[string]any{"append": cfg.SystemPrompt}}
	}
	cfg.SessionOptions = modelSessionOption
	driver := newDriverWith(cfg, agent)

	workspace := t.TempDir()
	conv, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID:              "worker-1",
		ProviderConversationID: "provider-session-1",
		WorkspacePath:          workspace,
		Env:                    map[string]string{"KEEP": "yes"},
		Model:                  "selected-resume-model",
		SystemPrompt:           "Recomputed Tallpine instructions",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer conv.Close()
	if got.SessionID != "worker-1" || got.WorkspacePath != workspace || got.Model != "selected-resume-model" ||
		got.Env["KEEP"] != "yes" || got.SystemPrompt != "Recomputed Tallpine instructions" {
		t.Fatalf("launch config = %#v", got)
	}
	agent.mu.Lock()
	resumeCalls, loadCalls := agent.resumeCalls, agent.loadCalls
	resumeMeta := agent.resumeParams.Meta
	resumeModel := agent.options["model"]
	agent.mu.Unlock()
	if resumeCalls != 1 || loadCalls != 0 {
		t.Fatalf("resume calls = %d, load calls = %d; want resume fallback", resumeCalls, loadCalls)
	}
	prompt, ok := resumeMeta["systemPrompt"].(map[string]any)
	if !ok || prompt["append"] != "Recomputed Tallpine instructions" {
		t.Fatalf("session/resume metadata = %#v, want recomputed system prompt", resumeMeta)
	}
	if resumeModel != "selected-resume-model" {
		t.Fatalf("resumed ACP model = %q, want selected-resume-model", resumeModel)
	}
	if conv.Capabilities().Has(ports.ChatCapabilityHistory) {
		t.Fatal("resume-only ACP conversation advertised replayable history")
	}
	if _, err := conv.(ports.ChatHistoryReader).ReadHistory(context.Background()); !errors.Is(err, ports.ErrChatHistoryUnavailable) {
		t.Fatalf("ReadHistory error = %v, want ErrChatHistoryUnavailable after session/resume", err)
	}
	if _, ok := conv.(ports.ChatHistoryRefresher); ok {
		t.Fatal("resume-only ACP conversation advertised refreshable history")
	}
}

// TestACPDriverDeliversSessionPreambleOnStartAndResume covers the channel Tallpine
// uses for agents that accept no system-prompt launch flag and ignore
// session/new _meta: an embedded resource leading the first prompt. Standing
// context is process input rather than transcript history, so a resumed session
// must receive the recomputed instructions again.
func TestACPDriverDeliversSessionPreambleOnStartAndResume(t *testing.T) {
	newDriver := func(agent *fakeAgent) *Driver {
		driver := New(preambleConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
		driver.useTestProcess(fakeSpawn(agent))
		return driver
	}

	t.Run("start", func(t *testing.T) {
		agent := &fakeAgent{promptNoPermission: true}
		conv, err := newDriver(agent).Start(context.Background(), ports.ChatStartConfig{
			WorkspacePath: t.TempDir(), SystemPrompt: standingInstructions,
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer conv.Close()
		_ = nextEvent(t, conv.Events()) // controller.ready
		prompt := runOneTurn(t, agent, conv, "do the task")
		if got := preambleText(prompt); got != standingInstructions {
			t.Fatalf("first prompt standing context = %q, want %q (prompt %#v)", got, standingInstructions, prompt)
		}
		if len(prompt) < 2 || prompt[1].Text == nil || prompt[1].Text.Text != "do the task" {
			t.Fatalf("standing context did not lead the user text: %#v", prompt)
		}
	})

	t.Run("resume", func(t *testing.T) {
		agent := &fakeAgent{promptNoPermission: true}
		conv, err := newDriver(agent).Resume(context.Background(), ports.ChatResumeConfig{
			SessionID:              "worker-1",
			ProviderConversationID: "provider-session-1",
			WorkspacePath:          t.TempDir(),
			SystemPrompt:           standingInstructions,
		})
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		defer conv.Close()
		_ = nextEvent(t, conv.Events()) // controller.ready
		prompt := runOneTurn(t, agent, conv, "continue the task")
		if got := preambleText(prompt); got != standingInstructions {
			t.Fatalf("resumed first prompt standing context = %q, want %q (prompt %#v)", got, standingInstructions, prompt)
		}
	})
}

// Standing context is process input, so it is delivered once per session rather
// than repeated on every turn, which would grow the transcript and re-state the
// same rules mid-conversation.
func TestACPDriverDeliversSessionPreambleOncePerSession(t *testing.T) {
	agent := &fakeAgent{promptNoPermission: true}
	conv := startPreambleConversation(t, preambleConfig(), agent)
	if got := preambleText(runOneTurn(t, agent, conv, "first")); got != standingInstructions {
		t.Fatalf("first prompt standing context = %q", got)
	}
	agent.mu.Lock()
	agent.promptParams = acpsdk.PromptRequest{}
	agent.mu.Unlock()
	if got := preambleText(runOneTurn(t, agent, conv, "second")); got != "" {
		t.Fatalf("second prompt repeated standing context %q; want it delivered once per session", got)
	}
}

// A first turn that never reaches the provider must not spend the standing
// context. Both paths here fail in SendTurn, after the prompt is built but
// before any session/prompt is sent: spending the preamble in SendTurn would
// silently drop Tallpine's instructions from the session's first real turn, which is
// the failure these cases exist to prevent.
func TestACPDriverPreservesSessionPreambleWhenFirstTurnNeverStarts(t *testing.T) {
	newConversation := func(t *testing.T, agent *fakeAgent) ports.ChatConversation {
		t.Helper()
		return startPreambleConversation(t, preambleConfig(), agent)
	}

	t.Run("rejected first-turn settings", func(t *testing.T) {
		agent := &fakeAgent{promptNoPermission: true}
		// Reject the first turn's settings, which fails SendTurn before the
		// prompt is ever sent to the provider. Session setup is unaffected.
		reject := false
		driverConfig := preambleConfig()
		driverConfig.ValidateTurnSettings = func(_ ports.PermissionMode, settings ports.ChatTurnSettings) error {
			if reject && settings.Model == "rejected-model" {
				return errors.New("settings rejected")
			}
			return nil
		}
		conv := startPreambleConversation(t, driverConfig, agent)

		reject = true
		if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{
			Text:     "do the task",
			Settings: ports.ChatTurnSettings{Model: "rejected-model"},
		}); err == nil {
			t.Fatal("SendTurn with rejected settings succeeded; want an error")
		}
		if agent.promptParams.Prompt != nil {
			t.Fatal("a turn whose settings were rejected still reached the provider")
		}

		// The retried turn must carry the standing context Tallpine never delivered.
		reject = false
		if got := preambleText(runOneTurn(t, agent, conv, "retry the task")); got != standingInstructions {
			t.Fatalf("retry standing context = %q, want %q; a rejected turn must not spend it", got, standingInstructions)
		}
	})

	t.Run("discarded prepared turn", func(t *testing.T) {
		agent := &fakeAgent{promptNoPermission: true}
		conv := newConversation(t, agent)

		// A prepared turn the controller drops before starting, as it does when
		// durable binding fails.
		ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{
			Text: "abandoned task", ClientMessageID: "tallpine-client-message-discarded",
		})
		if err != nil {
			t.Fatalf("SendTurn: %v", err)
		}
		conv.(ports.ChatDeferredTurnStarter).DiscardDeferredTurn(ref.ProviderTurnID)
		if agent.promptParams.Prompt != nil {
			t.Fatal("a discarded turn still reached the provider")
		}

		if got := preambleText(runOneTurn(t, agent, conv, "the real task")); got != standingInstructions {
			t.Fatalf("next prompt standing context = %q, want %q; a discarded turn must not spend it", got, standingInstructions)
		}
	})
}
