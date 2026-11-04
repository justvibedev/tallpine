package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestPersistentACPProviderHelper(t *testing.T) {
	if os.Getenv("TALLPINE_TEST_PERSISTENT_ACP_PROVIDER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	var parkedPromptID json.RawMessage
	promptCount := 0
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			continue
		}
		if request.Method == "" && string(request.ID) == `"permission-1"` && len(parkedPromptID) > 0 {
			recordPersistentACPCall("permission-response")
			_, _ = fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"persistent-provider-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":" approved"}}}}`)
			_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}`+"\n", parkedPromptID)
			parkedPromptID = nil
			continue
		}
		recordPersistentACPCall(request.Method)
		switch request.Method {
		case "initialize":
			if os.Getenv("TALLPINE_TEST_PERSISTENT_ACP_NO_RESUME") == "1" {
				_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"agentCapabilities":{},"authMethods":[]}}`+"\n", request.ID)
				continue
			}
			_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"agentCapabilities":{"sessionCapabilities":{"resume":{}}},"authMethods":[]}}`+"\n", request.ID)
		case "session/new":
			if os.Getenv("TALLPINE_TEST_PERSISTENT_ACP_BAD_SETUP") == "1" {
				_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{}}`+"\n", request.ID)
				continue
			}
			_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"persistent-provider-session"}}`+"\n", request.ID)
		case "session/prompt":
			promptCount++
			if promptCount == 1 && os.Getenv("TALLPINE_TEST_PERSISTENT_ACP_ERROR") == "1" {
				_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":"expired auth","data":"original provider data"}}`+"\n", request.ID)
				continue
			}
			_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"persistent-provider-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"provider-pid=%d survived"}}}}`+"\n", os.Getpid())
			if os.Getenv("TALLPINE_TEST_PERSISTENT_ACP_PERMISSION") == "1" {
				parkedPromptID = append(json.RawMessage(nil), request.ID...)
				recordPersistentACPCall("session/request_permission")
				_, _ = fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","id":"permission-1","method":"session/request_permission","params":{"sessionId":"persistent-provider-session","toolCall":{"toolCallId":"tool-1","title":"Approve restart","kind":"edit"},"options":[{"optionId":"allow","name":"Allow","kind":"allow_once"},{"optionId":"reject","name":"Reject","kind":"reject_once"}]}}`)
				continue
			}
			time.Sleep(200 * time.Millisecond)
			frames, _ := strconv.Atoi(os.Getenv("TALLPINE_TEST_PERSISTENT_ACP_REPLAY_FRAMES"))
			for range frames {
				_, _ = fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"persistent-provider-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"x"}}}}`)
			}
			_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}`+"\n", request.ID)
		default:
			if len(request.ID) > 0 {
				_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{}}`+"\n", request.ID)
			}
		}
	}
	os.Exit(0)
}

func TestPersistentACPDriverSurvivesRealProcessDetach(t *testing.T) {
	// These are protocol contract tests, not claims of authenticated E2E for
	// every vendor. Each runs the real detached host with a fake ACP process.
	for _, harness := range []domain.AgentHarness{
		domain.HarnessClaudeCode, domain.HarnessClaudeCode, domain.HarnessClaudeCode, domain.HarnessClaudeCode,
		domain.HarnessClaudeCode, domain.HarnessClaudeCode, domain.HarnessClaudeCode,
		domain.HarnessClaudeCode, domain.HarnessClaudeCode,
	} {
		t.Run(string(harness), func(t *testing.T) { testACPProcessDetach(t, harness, 0) })
	}
}

func TestPersistentACPLargeReplaySurvivesRestarts(t *testing.T) {
	testACPProcessDetach(t, domain.HarnessClaudeCode, 6375)
}

// A prompt attempted before the controller acknowledges the previous terminal
// receipt is refused by the host without a receipt of its own. That refusal must
// not erase the outstanding receipt, or the late ACK is dropped and the host
// refuses every later prompt.
func TestPersistentACPLateAckSurvivesHostLocalPromptRejection(t *testing.T) {
	for _, tc := range []struct {
		name          string
		env           map[string]string
		originalState domain.TurnState
	}{
		{name: "completed original", originalState: domain.TurnStateCompleted},
		{name: "failed original", env: map[string]string{"TALLPINE_TEST_PERSISTENT_ACP_ERROR": "1"},
			originalState: domain.TurnStateFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			callsPath := filepath.Join(t.TempDir(), "calls.log")
			env := map[string]string{
				"TALLPINE_TEST_PERSISTENT_ACP_PROVIDER": "1",
				"TALLPINE_TEST_PERSISTENT_ACP_CALLS":    callsPath,
			}
			for key, value := range tc.env {
				env[key] = value
			}
			driver := New(Config{
				Harness: domain.HarnessClaudeCode,
				Capabilities: ports.ChatCapabilities{
					ports.ChatCapabilityStreaming: true, ports.ChatCapabilityResume: true,
				},
				Launch: func(context.Context, LaunchConfig) (Launch, error) {
					return Launch{
						Command: os.Args[0], Args: []string{"-test.run=TestPersistentACPProviderHelper"}, Env: env,
					}, nil
				},
			}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			conv, err := driver.Start(context.Background(), ports.ChatStartConfig{
				SessionID: "persistent-acp-late-ack", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
				ProviderScopeID: "scope",
			})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer func() { _ = conv.(ports.ChatProviderTerminator).Terminate() }()
			ack := conv.(ports.ChatProviderEventAcknowledger)
			_ = nextEvent(t, conv.Events()) // controller.ready

			original := runPersistentACPTurn(t, conv)
			if original.TurnState != tc.originalState || original.ProviderEventID == "" {
				t.Fatalf("original terminal = %#v, want %s with a durable receipt", original, tc.originalState)
			}
			replacement := runPersistentACPTurn(t, conv)
			if replacement.TurnState != domain.TurnStateFailed || replacement.ProviderEventID != "" ||
				replacement.Err == nil || !strings.Contains(replacement.Err.Error(), "not acknowledged") {
				t.Fatalf("replacement terminal = %#v, want host-local unacknowledged rejection", replacement)
			}
			if err := ack.AcknowledgeProviderEvent(context.Background(), original.ProviderEventID); err != nil {
				t.Fatalf("acknowledge original: %v", err)
			}
			next := runPersistentACPTurn(t, conv)
			if next.TurnState != domain.TurnStateCompleted || next.ProviderEventID == "" {
				t.Fatalf("turn after late ACK = %#v, want completed", next)
			}
			if err := ack.AcknowledgeProviderEvent(context.Background(), next.ProviderEventID); err != nil {
				t.Fatalf("acknowledge next: %v", err)
			}
			calls, err := os.ReadFile(callsPath)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(string(calls), "session/prompt\n"); got != 2 {
				t.Fatalf("provider received %d prompts, want original and post-ACK turn only; calls:\n%s", got, calls)
			}
		})
	}
}

func TestPersistentACPDriverReplaysOnePermissionAndOriginalResponder(t *testing.T) {
	dataDir := t.TempDir()
	workdir := t.TempDir()
	callsPath := filepath.Join(t.TempDir(), "calls.log")
	cfg := Config{
		Harness: domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true, ports.ChatCapabilityApprovals: true,
			ports.ChatCapabilityResume: true,
		},
		Launch: func(context.Context, LaunchConfig) (Launch, error) {
			return Launch{
				Command: os.Args[0], Args: []string{"-test.run=TestPersistentACPProviderHelper"},
				Env: map[string]string{
					"TALLPINE_TEST_PERSISTENT_ACP_PROVIDER":   "1",
					"TALLPINE_TEST_PERSISTENT_ACP_PERMISSION": "1",
					"TALLPINE_TEST_PERSISTENT_ACP_CALLS":      callsPath,
				},
			}, nil
		},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	first, err := New(cfg, log).Start(context.Background(), ports.ChatStartConfig{
		SessionID: "persistent-acp-approval", DataDir: dataDir, WorkspacePath: workdir,
		ProviderScopeID: "scope",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = nextEvent(t, first.Events())
	ref, err := first.SendTurn(context.Background(), ports.ChatUserMessage{Text: "ask"})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatal(err)
	}
	var firstApproval ports.ChatEvent
	for firstApproval.Kind != ports.ChatEventApprovalRequested {
		firstApproval = nextEvent(t, first.Events())
	}
	if firstApproval.RequestID == "" {
		t.Fatalf("first approval = %#v", firstApproval)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	detachSettled := time.After(50 * time.Millisecond)
detachEvents:
	for {
		select {
		case event, ok := <-first.Events():
			if !ok {
				break detachEvents
			}
			if event.Kind == ports.ChatEventApprovalResolved {
				t.Fatalf("daemon detach falsely resolved pending approval: %#v", event)
			}
		case <-detachSettled:
			break detachEvents
		}
	}

	second, err := New(cfg, log).Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "persistent-acp-approval", DataDir: dataDir, WorkspacePath: workdir,
		ProviderConversationID: "persistent-provider-session", ProviderScopeID: "scope",
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer func() { _ = second.(ports.ChatProviderTerminator).Terminate() }()
	if err := second.(ports.ChatLiveReconnectActivator).ActivateLiveReconnect(context.Background(), ref.ProviderTurnID); err != nil {
		t.Fatal(err)
	}
	_ = nextEvent(t, second.Events())
	var replayedApproval ports.ChatEvent
	for replayedApproval.Kind != ports.ChatEventApprovalRequested {
		replayedApproval = nextEvent(t, second.Events())
	}
	if replayedApproval.RequestID != firstApproval.RequestID {
		t.Fatalf("approval identity changed across daemon: %q -> %q",
			firstApproval.RequestID, replayedApproval.RequestID)
	}
	if err := second.ResolveRequest(context.Background(), replayedApproval.RequestID, ports.ChatDecision{ID: "allow"}); err != nil {
		t.Fatalf("resolve replayed approval: %v", err)
	}
	var resolved, completed ports.ChatEvent
	for completed.Kind != ports.ChatEventTurnCompleted {
		event := nextEvent(t, second.Events())
		if event.Kind == ports.ChatEventApprovalResolved {
			resolved = event
		}
		if event.Kind == ports.ChatEventTurnCompleted {
			completed = event
		}
	}
	if resolved.ProviderEventID == "" {
		t.Fatalf("accepted approval has no stable provider event id: %#v", resolved)
	}
	if completed.TurnState != domain.TurnStateCompleted || completed.ProviderTurnID != ref.ProviderTurnID {
		t.Fatalf("completion after approval = %#v", completed)
	}
	if err := second.(ports.ChatProviderEventAcknowledger).AcknowledgeProviderEvent(context.Background(), completed.ProviderEventID); err != nil {
		t.Fatal(err)
	}

	calls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{
		"initialize", "session/new", "session/prompt", "session/request_permission", "permission-response",
	} {
		if got := strings.Count(string(calls), method+"\n"); got != 1 {
			t.Fatalf("provider method %s called %d times; calls:\n%s", method, got, calls)
		}
	}
}

func TestPersistentACPResumeAdoptsLivePromptWithoutSecondSetup(t *testing.T) {
	conv, host := resumePersistentConversation(t, ports.ChatCapabilities{ports.ChatCapabilityStreaming: true, ports.ChatCapabilityResume: true}, persistenthost.ACPState{ActivePrompt: true}, true)

	// The replacement SDK must not write initialize or session/resume. Its reader
	// also remains gated until the durable provider turn is installed.
	_ = host.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
	probe := make([]byte, 1)
	if _, err := host.Read(probe); err == nil {
		t.Fatal("replacement ACP client wrote setup traffic before activation")
	}
	_ = host.SetReadDeadline(time.Time{})
	if err := conv.ActivateLiveReconnect(context.Background(), "durable-turn"); err != nil {
		t.Fatalf("ActivateLiveReconnect: %v", err)
	}

	go func() {
		_, _ = fmt.Fprintln(host, `{"jsonrpc":"2.0","method":"session/update","params":{"_meta":{"ao.persistentEventId":"acp-host:1"},"sessionId":"provider-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"survived"}}}}`)
		_, _ = fmt.Fprintln(host, `{"jsonrpc":"2.0","method":"_ao/persistent_prompt_result","params":{"eventId":"acp-host:2","result":{"stopReason":"end_turn","_meta":{"ao.persistentEventId":"acp-host:2"}}}}`)
	}()

	ready := nextEvent(t, conv.Events())
	if ready.Kind != ports.ChatEventControllerState {
		t.Fatalf("setup event = %#v", ready)
	}
	delta := nextEvent(t, conv.Events())
	if delta.Kind != ports.ChatEventMessageDelta || delta.Delta != "survived" ||
		delta.ProviderTurnID != "durable-turn" || delta.ProviderEventID != "acp-host:1:0" {
		t.Fatalf("replayed delta = %#v", delta)
	}
	completed := nextEvent(t, conv.Events())
	for completed.Kind != ports.ChatEventTurnCompleted {
		completed = nextEvent(t, conv.Events())
	}
	if completed.ProviderTurnID != "durable-turn" || completed.TurnState != domain.TurnStateCompleted ||
		completed.ProviderEventID != "acp-host:2" {
		t.Fatalf("replayed completion = %#v", completed)
	}
	ackResult := make(chan []byte, 1)
	go func() {
		ack, _ := bufio.NewReader(host).ReadBytes('\n')
		ackResult <- ack
	}()
	if err := conv.AcknowledgeProviderEvent(context.Background(), completed.ProviderEventID); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}
	select {
	case ack := <-ackResult:
		if !strings.Contains(string(ack), "_ao/persistent_prompt_ack") {
			t.Fatalf("host ack = %q", ack)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for host ack")
	}
}

func TestPersistentACPReplayAppliesAcceptedPermissionCommand(t *testing.T) {
	conv, host := resumePersistentConversation(t, ports.ChatCapabilities{ports.ChatCapabilityStreaming: true, ports.ChatCapabilityApprovals: true, ports.ChatCapabilityResume: true}, persistenthost.ACPState{ActivePrompt: true}, true)
	if err := conv.ActivateLiveReconnect(context.Background(), "durable-turn"); err != nil {
		t.Fatalf("ActivateLiveReconnect: %v", err)
	}

	go func() {
		_, _ = fmt.Fprintln(host, `{"jsonrpc":"2.0","id":"permission-1","method":"session/request_permission","params":{"_meta":{"ao.persistentRequestId":"acp-request:1"},"sessionId":"provider-session","toolCall":{"toolCallId":"tool-1","title":"Approve"},"options":[{"optionId":"allow","name":"Allow","kind":"allow_once"}]}}`)
		_, _ = fmt.Fprintln(host, `{"jsonrpc":"2.0","method":"_ao/persistent_interaction_command","params":{"eventId":"acp-host:2","requestId":"acp-request:1","kind":"approval","providerPending":true,"decision":{"id":"allow"}}}`)
	}()

	_ = nextEvent(t, conv.Events()) // controller.ready
	requested := nextEvent(t, conv.Events())
	if requested.Kind != ports.ChatEventApprovalRequested || requested.RequestID != "acp-request:1" {
		t.Fatalf("requested event = %#v", requested)
	}
	resolved := nextEvent(t, conv.Events())
	if resolved.Kind != ports.ChatEventApprovalResolved || resolved.RequestID != requested.RequestID ||
		resolved.ProviderEventID != "acp-host:2" || !bytes.Contains(resolved.Detail, []byte(`"decision":"allow"`)) {
		t.Fatalf("resolved event = %#v", resolved)
	}

	if err := host.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	response, err := bufio.NewReader(host).ReadBytes('\n')
	if err != nil {
		t.Fatalf("provider permission response: %v", err)
	}
	if !bytes.Contains(response, []byte(`"id":"permission-1"`)) ||
		!bytes.Contains(response, []byte(`"optionId":"allow"`)) {
		t.Fatalf("provider permission response = %s", response)
	}
}

func TestPersistentACPReconnectAcknowledgesAlreadyCommittedPrompt(t *testing.T) {
	conv, host := resumePersistentConversation(t, ports.ChatCapabilities{ports.ChatCapabilityResume: true}, persistenthost.ACPState{PendingResultEventID: "acp-host:9"}, false)

	replayDone := make(chan error, 1)
	go func() {
		_, writeErr := fmt.Fprintln(host, `{"jsonrpc":"2.0","method":"_ao/persistent_prompt_result","params":{"eventId":"acp-host:9","result":{"stopReason":"end_turn"}}}`)
		replayDone <- writeErr
	}()
	ackResult := make(chan []byte, 1)
	go func() {
		ack, _ := bufio.NewReader(host).ReadBytes('\n')
		ackResult <- ack
	}()

	// No durable running turn means the terminal event was already committed.
	// Reconnect must close the commit/ACK crash window without projecting it a
	// second time or becoming permanently unrecoverable.
	if err := conv.ActivateLiveReconnect(context.Background(), ""); err != nil {
		t.Fatalf("ActivateLiveReconnect: %v", err)
	}
	select {
	case ack := <-ackResult:
		if !strings.Contains(string(ack), `"eventId":"acp-host:9"`) {
			t.Fatalf("ack = %s", ack)
		}
	case <-time.After(time.Second):
		t.Fatal("committed prompt result was not acknowledged")
	}
	if err := <-replayDone; err != nil {
		t.Fatalf("write replay: %v", err)
	}
	ready := nextEvent(t, conv.Events())
	if ready.Kind != ports.ChatEventControllerState || ready.ControllerState != ports.ChatControllerReady {
		t.Fatalf("setup event = %#v", ready)
	}
	select {
	case event := <-conv.Events():
		if event.Kind == ports.ChatEventTurnCompleted {
			t.Fatalf("already committed result projected again: %#v", event)
		}
	case <-time.After(100 * time.Millisecond):
	}
}

// resumePersistentConversation resumes a conversation over a reconnected
// persistent host whose ACP state is state. The returned conn is the host end
// of the pipe; the daemon end is wired into the driver.
func resumePersistentConversation(t *testing.T, caps ports.ChatCapabilities, state persistenthost.ACPState, withScope bool) (*conversation, net.Conn) {
	t.Helper()
	initialize, err := json.Marshal(acpsdk.InitializeResponse{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		AgentCapabilities: acpsdk.AgentCapabilities{
			SessionCapabilities: acpsdk.SessionCapabilities{Resume: &acpsdk.SessionResumeCapabilities{}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := json.Marshal(acpsdk.NewSessionResponse{SessionId: "provider-session"})
	if err != nil {
		t.Fatal(err)
	}
	daemon, host := net.Pipe()
	t.Cleanup(func() { _ = host.Close() })
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: caps,
		Launch: func(context.Context, LaunchConfig) (Launch, error) {
			return Launch{Command: "fake"}, nil
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	state.InitializeResult, state.SessionResult, state.SessionID = initialize, session, "provider-session"
	driver.connectHost = func(context.Context, persistenthost.Config) (*persistenthost.Transport, error) {
		return &persistenthost.Transport{Stdin: daemon, Stdout: daemon, Reconnected: true, ACPState: &state}, nil
	}
	cfg := ports.ChatResumeConfig{
		SessionID: "tallpine-session", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		ProviderConversationID: "provider-session",
	}
	if withScope {
		cfg.ProviderScopeID = "scope"
	}
	opened, err := driver.Resume(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	conv := opened.(*conversation)
	t.Cleanup(func() { _ = conv.Close() })
	return conv, host
}
