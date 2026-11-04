package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestACPDriverRefreshesHistoryWithAnotherSessionLoad(t *testing.T) {
	userOneID := "11111111-1111-4111-8111-111111111111"
	answerOneID := "22222222-2222-4222-8222-222222222222"
	userTwoID := "33333333-3333-4333-8333-333333333333"
	answerTwoID := "44444444-4444-4444-8444-444444444444"
	userOne := acpsdk.UpdateUserMessageText("Inspect the repository")
	userOne.UserMessageChunk.MessageId = &userOneID
	replayBlock := acpsdk.ResourceBlock(acpsdk.EmbeddedResourceResource{
		TextResourceContents: &acpsdk.TextResourceContents{
			Uri: ports.ChatInternalReplayResourceURI, Text: `{"kind":"approximate_conversation_context"}`,
			Meta: map[string]any{tallpineInternalReplayMetaKey: true},
		},
	})
	replaySeed := acpsdk.UpdateUserMessage(replayBlock)
	replaySeed.UserMessageChunk.MessageId = &userOneID
	answerOneA := acpsdk.UpdateAgentMessageText("The repository ")
	answerOneA.AgentMessageChunk.MessageId = &answerOneID
	answerOneB := acpsdk.UpdateAgentMessageText("is ready.")
	answerOneB.AgentMessageChunk.MessageId = &answerOneID
	userTwo := acpsdk.UpdateUserMessageText("Run the tests")
	userTwo.UserMessageChunk.MessageId = &userTwoID
	answerTwo := acpsdk.UpdateAgentMessageText("All tests pass.")
	answerTwo.AgentMessageChunk.MessageId = &answerTwoID
	pendingTool := acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
		SessionUpdate: "tool_call", ToolCallId: "history-tool", Title: "Run tests",
		Kind: acpsdk.ToolKindExecute, Status: acpsdk.ToolCallStatusInProgress,
	}}

	agent := &fakeAgent{
		capabilities: &acpsdk.AgentCapabilities{
			LoadSession: true,
		},
		loadUpdateBatches: [][]acpsdk.SessionUpdate{
			{userOne, replaySeed, answerOneA, answerOneB},
			{userOne, replaySeed, answerOneA, answerOneB, userTwo, answerTwo, pendingTool},
			{userOne, replaySeed, answerOneA, answerOneB, userTwo, answerTwo, pendingTool},
		},
	}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SessionMeta: func(cfg LaunchConfig) map[string]any {
			return map[string]any{"systemPrompt": map[string]any{"append": cfg.SystemPrompt}}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	conv, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
		ProviderConversationID: "provider-session-1",
		WorkspacePath:          t.TempDir(),
		SystemPrompt:           "Tallpine load instructions",
		ProviderScopeID:        "history-scope",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer conv.Close()
	refresher, ok := conv.(ports.ChatHistoryRefresher)
	if !ok {
		t.Fatal("load-capable ACP conversation does not advertise refreshable history")
	}

	agent.mu.Lock()
	loadCalls, resumeCalls := agent.loadCalls, agent.resumeCalls
	loadedSession := string(agent.loadParams.SessionId)
	loadMeta := agent.loadParams.Meta
	agent.mu.Unlock()
	if loadCalls != 1 || resumeCalls != 0 || loadedSession != "provider-session-1" {
		t.Fatalf("load calls = %d, resume calls = %d, session = %q", loadCalls, resumeCalls, loadedSession)
	}
	prompt, ok := loadMeta["systemPrompt"].(map[string]any)
	if !ok || prompt["append"] != "Tallpine load instructions" {
		t.Fatalf("session/load metadata = %#v, want recomputed system prompt", loadMeta)
	}

	initial, err := conv.(ports.ChatHistoryReader).ReadHistory(context.Background())
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	initialTurns := 0
	for _, event := range initial {
		if event.Kind == ports.ChatEventUserMessageCompleted && event.NativeUserMessageID != userOneID {
			t.Fatalf("replay lost native user identity: %+v", event)
		}
		if event.Kind == ports.ChatEventTurnCompleted {
			initialTurns++
		}
	}
	if initialTurns != 1 {
		t.Fatalf("initial completed turns = %d, want only the first replayed turn", initialTurns)
	}
	history, err := refresher.RefreshHistory(context.Background())
	if err != nil {
		t.Fatalf("RefreshHistory: %v", err)
	}
	agent.mu.Lock()
	loadCalls = agent.loadCalls
	agent.mu.Unlock()
	if loadCalls != 2 {
		t.Fatalf("session/load calls = %d, want a fresh provider observation", loadCalls)
	}
	if len(history) <= len(initial) {
		t.Fatalf("refreshed history has %d events, want more than initial snapshot's %d", len(history), len(initial))
	}
	for i := range initial {
		if initial[i].ProviderEventID != history[i].ProviderEventID {
			t.Fatalf("event %d identity changed across replay: %q != %q",
				i, initial[i].ProviderEventID, history[i].ProviderEventID)
		}
	}
	identical, err := refresher.RefreshHistory(context.Background())
	if err != nil {
		t.Fatalf("RefreshHistory identical replay: %v", err)
	}
	agent.mu.Lock()
	loadCalls = agent.loadCalls
	agent.mu.Unlock()
	if loadCalls != 3 {
		t.Fatalf("session/load calls = %d after identical refresh, want another provider observation", loadCalls)
	}
	if len(identical) != len(history) {
		t.Fatalf("identical replay has %d events, want %d", len(identical), len(history))
	}
	for i := range history {
		if history[i].ProviderEventID != identical[i].ProviderEventID {
			t.Fatalf("identical replay event %d changed identity: %q != %q",
				i, history[i].ProviderEventID, identical[i].ProviderEventID)
		}
	}
	var states []domain.TurnState
	var recoveredActivity bool
	for _, event := range history {
		if event.Kind == ports.ChatEventTurnCompleted {
			states = append(states, event.TurnState)
		}
		if event.ProviderItemID == (&conversation{providerScopeID: "history-scope"}).providerItemID("history-tool") &&
			event.Kind == ports.ChatEventActivityCompleted {
			recoveredActivity = event.ActivityStatus == domain.ActivityStatusRecovered
		}
	}
	if len(states) != 2 || states[0] != domain.TurnStateRecovered ||
		states[1] != domain.TurnStateRecovered {
		t.Fatalf("replayed turn states = %v, want [recovered recovered]", states)
	}
	if !recoveredActivity {
		t.Fatalf("history = %#v, want pending replay tool settled as recovered", history)
	}
	if history[0].ProviderTurnID != historyTurnID("history-scope", userOneID) ||
		history[1].ProviderItemID != (&conversation{providerScopeID: "history-scope"}).providerItemID(userOneID) ||
		history[1].ClientMessageID != (&conversation{providerScopeID: "history-scope"}).providerItemID(userOneID) ||
		history[2].ProviderItemID != (&conversation{providerScopeID: "history-scope"}).providerItemID(answerOneID) {
		t.Fatalf("history ids do not use the durable provider scope: %#v", history[:3])
	}
	if len(history[1].ProviderItemAliases) != 1 || history[1].ProviderItemAliases[0] != userOneID ||
		len(history[2].ProviderItemAliases) != 1 || history[2].ProviderItemAliases[0] != answerOneID {
		t.Fatalf("history does not preserve legacy raw item aliases: %#v", history[:3])
	}
	if !conv.Capabilities().Has(ports.ChatCapabilityHistory) {
		t.Fatal("session/load conversation did not advertise replayable history")
	}

	ready := nextEvent(t, conv.Events())
	if ready.Kind != ports.ChatEventControllerState || ready.ControllerState != ports.ChatControllerReady {
		t.Fatalf("first live event = %#v, want controller ready", ready)
	}
	select {
	case event := <-conv.Events():
		t.Fatalf("history leaked onto the live event stream: %#v", event)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestACPDriverHistoryRefreshHonorsCancellation(t *testing.T) {
	userID := "11111111-1111-4111-8111-111111111111"
	user := acpsdk.UpdateUserMessageText("Inspect the repository")
	user.UserMessageChunk.MessageId = &userID
	refreshStarted := make(chan struct{})
	agent := &fakeAgent{
		capabilities:  &acpsdk.AgentCapabilities{LoadSession: true},
		loadUpdates:   []acpsdk.SessionUpdate{user},
		blockLoadCall: 2,
		loadStarted:   refreshStarted,
	}
	driver := newFakeDriver(agent)

	conv, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
		ProviderConversationID: "provider-session-1",
		WorkspacePath:          t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer conv.Close()
	refresher := conv.(ports.ChatHistoryRefresher)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, refreshErr := refresher.RefreshHistory(ctx)
		done <- refreshErr
	}()
	<-refreshStarted
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("RefreshHistory error = %v, want context cancellation", err)
	}
}

// A provider that answers session/load with -32603 while Tallpine's context is still
// live has failed the replay itself; the refresh must return a dedicated error
// rather than a generic one the settle loop keeps polling.
func TestACPDriverHistoryRefreshMapsProviderInternalErrorToLoadFailed(t *testing.T) {
	userID := "11111111-1111-4111-8111-111111111111"
	user := acpsdk.UpdateUserMessageText("Inspect the repository")
	user.UserMessageChunk.MessageId = &userID
	agent := &fakeAgent{
		capabilities: &acpsdk.AgentCapabilities{LoadSession: true},
		loadUpdates:  []acpsdk.SessionUpdate{user},
		failLoadFrom: 2,
		failLoadErr:  context.DeadlineExceeded,
	}
	driver := newFakeDriver(agent)

	conv, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
		ProviderConversationID: "provider-session-1",
		WorkspacePath:          t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer conv.Close()

	_, err = conv.(ports.ChatHistoryRefresher).RefreshHistory(context.Background())
	if !errors.Is(err, ports.ErrChatHistoryLoadFailed) {
		t.Fatalf("RefreshHistory error = %v, want ErrChatHistoryLoadFailed", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RefreshHistory error = %v, provider's own deadline must not read as Tallpine's", err)
	}
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) || requestErr.Code != -32603 {
		t.Fatalf("RefreshHistory error = %v, want the provider's -32603 preserved", err)
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("RefreshHistory error = %v, want the provider detail preserved", err)
	}
}

// When Tallpine's own deadline ends a session/load, the SDK reports a synthetic
// -32603 carrying "context deadline exceeded". That is Tallpine's timeout, not a
// provider failure, and must keep its context error classification.
func TestACPDriverHistoryRefreshKeepsOwnDeadlineAsContextError(t *testing.T) {
	userID := "11111111-1111-4111-8111-111111111111"
	user := acpsdk.UpdateUserMessageText("Inspect the repository")
	user.UserMessageChunk.MessageId = &userID
	agent := &fakeAgent{
		capabilities:  &acpsdk.AgentCapabilities{LoadSession: true},
		loadUpdates:   []acpsdk.SessionUpdate{user},
		blockLoadCall: 2,
	}
	driver := newFakeDriver(agent)

	conv, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
		ProviderConversationID: "provider-session-1",
		WorkspacePath:          t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer conv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = conv.(ports.ChatHistoryRefresher).RefreshHistory(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RefreshHistory error = %v, want Tallpine's own deadline", err)
	}
	if errors.Is(err, ports.ErrChatHistoryLoadFailed) {
		t.Fatalf("RefreshHistory error = %v, Tallpine's own deadline must not read as a provider load failure", err)
	}
}

// The initial resume load takes the same mapping so a transition that fails
// before the first refresh also reports the rejection.
func TestACPDriverResumeMapsProviderInternalLoadErrorToLoadFailed(t *testing.T) {
	agent := &fakeAgent{
		capabilities: &acpsdk.AgentCapabilities{LoadSession: true},
		failLoadFrom: 1,
		failLoadErr:  errors.New("transcript replay failed"),
	}
	driver := newFakeDriver(agent)

	_, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
		ProviderConversationID: "provider-session-1",
		WorkspacePath:          t.TempDir(),
	})
	if !errors.Is(err, ports.ErrChatResumeFailed) || !errors.Is(err, ports.ErrChatHistoryLoadFailed) {
		t.Fatalf("Resume error = %v, want ErrChatResumeFailed wrapping ErrChatHistoryLoadFailed", err)
	}
}

// A failed session/load must log its cause: a bare outcome=error cannot tell a
// provider refusal from Tallpine's own handshake deadline.
func TestACPDriverResumeLogsSessionLoadErrorText(t *testing.T) {
	agent := &fakeAgent{
		capabilities: &acpsdk.AgentCapabilities{LoadSession: true},
		failLoadFrom: 1,
		failLoadErr:  errors.New("transcript replay failed"),
	}
	var logs bytes.Buffer
	driver := New(fakeConfig(), slog.New(slog.NewTextHandler(&logs, nil)))
	driver.useTestProcess(fakeSpawn(agent))

	if _, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
		ProviderConversationID: "provider-session-1",
		WorkspacePath:          t.TempDir(),
	}); err == nil {
		t.Fatal("Resume succeeded, want a session/load failure")
	}
	var line string
	for _, l := range strings.Split(logs.String(), "\n") {
		if strings.Contains(l, "stage=session_load") {
			line = l
		}
	}
	if !strings.Contains(line, "outcome=error") || !strings.Contains(line, "transcript replay failed") {
		t.Fatalf("session_load log = %q, want outcome=error with the provider's error text", line)
	}
}

func TestNormalizeACPLoadErrorKeepsOtherCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "auth required", err: acpsdk.NewAuthRequired(nil), want: ports.ErrChatAuthRequired},
		{name: "internal", err: acpsdk.NewInternalError(map[string]any{"error": "context deadline exceeded"}), want: ports.ErrChatHistoryLoadFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := normalizeACPLoadError("ACP session/load", tt.err)
			if !errors.Is(err, tt.want) {
				t.Fatalf("normalizeACPLoadError = %v, want %v", err, tt.want)
			}
		})
	}
	plain := errors.New("peer disconnected before response")
	if err := normalizeACPLoadError("ACP session/load", plain); errors.Is(err, ports.ErrChatHistoryLoadFailed) || !errors.Is(err, plain) {
		t.Fatalf("normalizeACPLoadError(plain) = %v, want the transport error untouched", err)
	}
	if err := normalizeACPError("ACP session/prompt", acpsdk.NewInternalError(nil)); errors.Is(err, ports.ErrChatHistoryLoadFailed) {
		t.Fatalf("normalizeACPError(prompt -32603) = %v, must not read as a history load rejection", err)
	}
}

func TestHistoricalUserContentDropsStandingInstructionsJoinedToTheFirstMessage(t *testing.T) {
	standing := StandingInstructionsBlock("Tallpine standing instructions")
	// Cursor replays the first prompt's text blocks joined into one.
	joined := acpsdk.TextBlock(standing.Text.Text + "\nFix the failing test")
	if got := historicalUserContent(joined); got != "Fix the failing test" {
		t.Fatalf("replayed first message = %q, want only what the user typed", got)
	}
	if got := historicalUserContent(acpsdk.TextBlock("Fix the failing test")); got != "Fix the failing test" {
		t.Fatalf("ordinary message = %q", got)
	}
}

func TestHistoricalUserContentSuppressesOnlyMarkedInternalReplayResources(t *testing.T) {
	resource := acpsdk.ResourceBlock(acpsdk.EmbeddedResourceResource{
		TextResourceContents: &acpsdk.TextResourceContents{
			Uri: ports.ChatInternalReplayResourceURI, Text: `{"kind":"approximate_conversation_context"}`,
		},
	})
	if got := historicalUserContent(resource); got != "[Embedded context]" {
		t.Fatalf("unmarked reserved resource = %q, want visible embedded context", got)
	}

	resource.Resource.Resource.TextResourceContents.Meta = map[string]any{tallpineInternalReplayMetaKey: true}
	if got := historicalUserContent(resource); got != "" {
		t.Fatalf("marked internal replay resource = %q, want hidden", got)
	}
}

func TestACPDriverClosesTrailingUserOnlyHistoryAsRecovered(t *testing.T) {
	userID := "55555555-5555-4555-8555-555555555555"
	user := acpsdk.UpdateUserMessageText("Work that has not produced a provider event yet")
	user.UserMessageChunk.MessageId = &userID
	agent := &fakeAgent{
		capabilities: &acpsdk.AgentCapabilities{
			LoadSession: true,
			SessionCapabilities: acpsdk.SessionCapabilities{
				Resume: &acpsdk.SessionResumeCapabilities{},
			},
		},
		loadUpdates: []acpsdk.SessionUpdate{user},
	}
	driver := newFakeDriver(agent)

	conv, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
		ProviderConversationID: "provider-session-1",
		WorkspacePath:          t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer conv.Close()

	history, err := conv.(ports.ChatHistoryReader).ReadHistory(context.Background())
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(history) != 3 || history[2].Kind != ports.ChatEventTurnCompleted ||
		history[2].TurnState != domain.TurnStateRecovered {
		t.Fatalf("history = %#v, want trailing recovered turn", history)
	}
}

func TestConversationCapabilitiesTreatLoadSessionAsResume(t *testing.T) {
	configured := ports.ChatCapabilities{ports.ChatCapabilityResume: true}
	init := acpsdk.InitializeResponse{AgentCapabilities: acpsdk.AgentCapabilities{
		LoadSession: true,
	}}
	if !conversationCapabilities(configured, init)[ports.ChatCapabilityResume] {
		t.Fatal("loadSession-only ACP agent was reported as non-resumable")
	}
}

func TestACPDriverNamespacesOpaqueItemIDsByProviderScope(t *testing.T) {
	type observed struct {
		messageID string
		toolID    string
		parentID  string
	}
	open := func(t *testing.T, providerScopeID string) observed {
		t.Helper()
		agent := &fakeAgent{}
		driver := newFakeDriver(agent)
		opened, err := driver.Start(context.Background(), ports.ChatStartConfig{
			SessionID: domain.SessionID("session-1"), WorkspacePath: t.TempDir(),
			ProviderScopeID: providerScopeID,
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Cleanup(func() { _ = opened.Close() })
		_ = nextEvent(t, opened.Events())
		conv := opened.(*conversation)
		conv.mu.Lock()
		conv.activeTurn = "turn-1"
		conv.mu.Unlock()

		messageID := "reused-message"
		message := acpsdk.UpdateAgentMessageText("working")
		message.AgentMessageChunk.MessageId = &messageID
		if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
			SessionId: acpsdk.SessionId(opened.ProviderConversationID()), Update: message,
		}); err != nil {
			t.Fatalf("message update: %v", err)
		}
		messageEvent := nextEvent(t, opened.Events())

		if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
			SessionId: acpsdk.SessionId(opened.ProviderConversationID()),
			Update: acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
				SessionUpdate: "tool_call", ToolCallId: "reused-tool", Title: "Run tests",
				Kind: acpsdk.ToolKindExecute, Status: acpsdk.ToolCallStatusPending,
				Meta: map[string]any{"claudeCode": map[string]any{"parentToolUseId": "reused-parent"}},
			}},
		}); err != nil {
			t.Fatalf("tool update: %v", err)
		}
		toolEvent := nextEvent(t, opened.Events())
		var detail map[string]any
		if err := json.Unmarshal(toolEvent.Detail, &detail); err != nil {
			t.Fatalf("tool detail: %v", err)
		}
		parentID, _ := detail["parentProviderItemId"].(string)
		return observed{
			messageID: messageEvent.ProviderItemID,
			toolID:    toolEvent.ProviderItemID,
			parentID:  parentID,
		}
	}

	first := open(t, "scope-one")
	second := open(t, "scope-two")
	if first.messageID == second.messageID || first.toolID == second.toolID || first.parentID == second.parentID {
		t.Fatalf("provider scopes reused opaque ids: first=%+v second=%+v", first, second)
	}
	firstScope := &conversation{providerScopeID: "scope-one"}
	if first.messageID != firstScope.providerItemID("reused-message") ||
		first.toolID != firstScope.providerItemID("reused-tool") ||
		first.parentID != firstScope.providerItemID("reused-parent") {
		t.Fatalf("first scoped ids = %+v", first)
	}
	secondScope := &conversation{providerScopeID: "scope-two"}
	if second.messageID != secondScope.providerItemID("reused-message") ||
		second.toolID != secondScope.providerItemID("reused-tool") ||
		second.parentID != secondScope.providerItemID("reused-parent") {
		t.Fatalf("second scoped ids = %+v", second)
	}
}

func TestACPOpaqueIDNamespaceIsUnambiguous(t *testing.T) {
	first := &conversation{providerScopeID: "a"}
	second := &conversation{providerScopeID: "a:b"}

	firstID := first.providerItemID("b:c")
	secondID := second.providerItemID("c")
	if firstID == secondID {
		t.Fatalf("distinct scope/id pairs collided at %q", firstID)
	}

	firstTurn := historyTurnID("a", "b:c")
	secondTurn := historyTurnID("a:b", "c")
	if firstTurn == secondTurn {
		t.Fatalf("distinct history scope/id pairs collided at %q", firstTurn)
	}
}

func TestACPLegacyUnscopedHistoryIdentityRemainsCompatible(t *testing.T) {
	conv := &conversation{
		history: &historyCapture{
			sessionID: "legacy-session", occurrences: make(map[string]int),
		},
	}
	conv.startHistoryTurn("legacy-user")
	if got := conv.history.turnID; got != "acp-history-turn:legacy-session:legacy-user" {
		t.Fatalf("legacy history turn = %q", got)
	}
	if got := conv.providerItemID("legacy-item"); got != "legacy-item" {
		t.Fatalf("legacy provider item = %q, want raw identity", got)
	}
}

func TestACPHistoryEventIdentityIsUnambiguousWithNULInOpaqueIDs(t *testing.T) {
	capture := func(event ports.ChatEvent) string {
		t.Helper()
		conv := &conversation{
			providerScopeID: "scope",
			history: &historyCapture{
				sessionID:   "session",
				occurrences: make(map[string]int),
			},
		}
		if !conv.captureHistoryEvent(event) {
			t.Fatal("captureHistoryEvent did not capture replay event")
		}
		return conv.history.events[0].ProviderEventID
	}

	first := capture(ports.ChatEvent{
		Kind: ports.ChatEventMessageDelta, ProviderTurnID: "a", ProviderItemID: "b\x00c",
	})
	second := capture(ports.ChatEvent{
		Kind: ports.ChatEventMessageDelta, ProviderTurnID: "a\x00b", ProviderItemID: "c",
	})
	if first == second {
		t.Fatalf("distinct history events collided at %q", first)
	}
}

func TestReconnectMissingHostNeverLaunchesProvider(t *testing.T) {
	driver := New(Config{Harness: domain.HarnessClaudeCode, Launch: func(context.Context, LaunchConfig) (Launch, error) {
		t.Fatal("health check tried to launch ACP provider")
		return Launch{}, nil
	}}, nil)
	_, err := driver.Reconnect(context.Background(), ports.ChatResumeConfig{
		SessionID: "stopped", ProviderConversationID: "native", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		PrepareEnv: func(context.Context) (map[string]string, error) {
			t.Fatal("health check rotated launch credentials")
			return nil, nil
		},
	})
	if !errors.Is(err, ports.ErrChatHostNotRunning) {
		t.Fatalf("error=%v", err)
	}
}

// A provider that never persisted the stored session reports it missing on
// reload. Only a caller that proved the conversation never started may then
// start fresh; every other reload failure keeps its existing error.
func TestACPDriverResumeStartsFreshOnlyForMissingSessionWhenAllowed(t *testing.T) {
	notFound := &acpsdk.RequestError{Code: -32002, Message: "Resource not found: provider-session-1"}
	tests := []struct {
		name           string
		loadErr        error
		freshIfMissing bool
		wantFresh      bool
	}{
		{name: "missing and allowed", loadErr: notFound, freshIfMissing: true, wantFresh: true},
		{name: "missing without proof", loadErr: notFound},
		{name: "provider failure with proof", loadErr: errors.New("transcript replay failed"), freshIfMissing: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &fakeAgent{
				capabilities: &acpsdk.AgentCapabilities{LoadSession: true},
				failLoadFrom: 1,
				failLoadErr:  tt.loadErr,
			}
			driver := newFakeDriver(agent)

			conv, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
				ProviderConversationID: "provider-session-1",
				WorkspacePath:          t.TempDir(),
				FreshIfMissing:         tt.freshIfMissing,
			})
			if !tt.wantFresh {
				if !errors.Is(err, ports.ErrChatResumeFailed) {
					t.Fatalf("Resume error = %v, want ErrChatResumeFailed", err)
				}
				agent.mu.Lock()
				newCwd := agent.newParams.Cwd
				agent.mu.Unlock()
				if newCwd != "" {
					t.Fatal("a reload failure without proof started a fresh provider session")
				}
				return
			}
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			defer func() { _ = conv.Close() }()
			if got := conv.ProviderConversationID(); got != "claude-session-1" {
				t.Fatalf("provider conversation = %q, want the fresh session", got)
			}
		})
	}
}

// An agent that refuses to reload a session its connection holds (Copilot)
// still refreshes history when the binding closes the session first.
func TestACPRefreshHistoryClosesSessionBeforeReload(t *testing.T) {
	for _, closeFirst := range []bool{false, true} {
		agent := &fakeAgent{
			rejectReload: true,
			capabilities: &acpsdk.AgentCapabilities{
				LoadSession:         true,
				SessionCapabilities: acpsdk.SessionCapabilities{Close: &acpsdk.SessionCloseCapabilities{}},
			},
		}
		driver := New(Config{
			Harness:                  domain.HarnessClaudeCode,
			Capabilities:             ports.ChatCapabilities{ports.ChatCapabilityStreaming: true, ports.ChatCapabilityResume: true},
			Probe:                    func(context.Context) error { return nil },
			Launch:                   func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
			CloseSessionBeforeReload: closeFirst,
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		driver.useTestProcess(fakeSpawn(agent))
		conv, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
			SessionID: "sess1", WorkspacePath: t.TempDir(), ProviderConversationID: "provider-session-1",
		})
		if err != nil {
			t.Fatalf("closeFirst=%v Resume: %v", closeFirst, err)
		}
		_, err = conv.(ports.ChatHistoryRefresher).RefreshHistory(context.Background())
		conv.Close()
		agent.mu.Lock()
		closes, loads := agent.closeCalls, agent.loadCalls
		agent.mu.Unlock()
		if !closeFirst {
			if err == nil {
				t.Fatal("without CloseSessionBeforeReload the reload should hit the agent's already-loaded error")
			}
			continue
		}
		if err != nil {
			t.Fatalf("RefreshHistory: %v", err)
		}
		if loads != 2 || closes < 1 {
			t.Fatalf("loads = %d, closes = %d; want 2 loads with a close between", loads, closes)
		}
	}
}

// Copilot replays user prompts without message ids. Each prompt after an
// answer must start its own turn, or every later turn merges into the first
// and no checkpoint can match.
func TestACPHistoryReplaySplitsIDLessPromptsAfterAnswers(t *testing.T) {
	agent := &fakeAgent{
		capabilities: &acpsdk.AgentCapabilities{LoadSession: true},
		loadUpdates: []acpsdk.SessionUpdate{
			acpsdk.UpdateUserMessageText("Hi how are "),
			acpsdk.UpdateUserMessageText("you"),
			acpsdk.UpdateAgentMessageText("Doing well."),
			acpsdk.UpdateUserMessageText("What did I ask?"),
			acpsdk.UpdateAgentMessageText("You asked how I am."),
		},
	}
	driver := newFakeDriver(agent, ports.ChatCapabilityResume)
	conv, err := driver.Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "sess1", WorkspacePath: t.TempDir(), ProviderConversationID: "provider-session-1",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer conv.Close()
	events, err := conv.(ports.ChatHistoryReader).ReadHistory(context.Background())
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	type turn struct{ user, assistant string }
	var turns []turn
	byID := map[string]int{}
	for _, event := range events {
		index, ok := byID[event.ProviderTurnID]
		if !ok {
			index = len(turns)
			byID[event.ProviderTurnID] = index
			turns = append(turns, turn{})
		}
		switch event.Kind {
		case ports.ChatEventUserMessageCompleted:
			turns[index].user += event.Text
		case ports.ChatEventMessageCompleted:
			turns[index].assistant += event.Text
		}
	}
	want := []turn{{"Hi how are you", "Doing well."}, {"What did I ask?", "You asked how I am."}}
	if !reflect.DeepEqual(turns, want) {
		t.Fatalf("replayed turns = %+v, want %+v", turns, want)
	}
}
