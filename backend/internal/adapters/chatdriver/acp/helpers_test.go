package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/testenv"
)

func TestMain(m *testing.M) {
	if len(os.Args) >= 7 && os.Args[1] == "chat-host" {
		protocol := persistenthost.ProtocolRaw
		fingerprint := ""
		separator := 5
		if os.Args[5] == string(persistenthost.ProtocolACP) {
			protocol = persistenthost.ProtocolACP
			if len(os.Args) > 6 {
				fingerprint = os.Args[6]
			}
			separator = 7
		}
		if len(os.Args) <= separator || os.Args[separator] != "--" {
			os.Exit(2)
		}
		err := persistenthost.Run(context.Background(), persistenthost.Config{
			SessionID: os.Args[2], DataDir: os.Args[3], Workdir: os.Args[4],
			Env: os.Environ(), Argv: os.Args[separator+1:], Protocol: protocol,
			OwnershipFingerprint: fingerprint,
		})
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	testenv.Main(m)
}

func recordPersistentACPCall(method string) {
	path := os.Getenv("TALLPINE_TEST_PERSISTENT_ACP_CALLS")
	if path == "" || method == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(f, method)
	_ = f.Close()
}

func testACPProcessDetach(t *testing.T, harness domain.AgentHarness, replayFrames int) {
	dataDir := t.TempDir()
	workdir := t.TempDir()
	callsPath := filepath.Join(t.TempDir(), "calls.log")
	prepareCalls := 0
	prepareEnv := func(context.Context) (map[string]string, error) {
		prepareCalls++
		return map[string]string{"TALLPINE_BROWSER_CAPABILITY": fmt.Sprintf("token-%d", prepareCalls)}, nil
	}
	cfg := Config{
		Harness: harness,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true, ports.ChatCapabilityResume: true,
		},
		Launch: func(context.Context, LaunchConfig) (Launch, error) {
			return Launch{
				Command: os.Args[0], Args: []string{"-test.run=TestPersistentACPProviderHelper"},
				Env: map[string]string{
					"TALLPINE_TEST_PERSISTENT_ACP_PROVIDER":      "1",
					"TALLPINE_TEST_PERSISTENT_ACP_NO_RESUME":     "1",
					"TALLPINE_TEST_PERSISTENT_ACP_CALLS":         callsPath,
					"TALLPINE_TEST_PERSISTENT_ACP_REPLAY_FRAMES": strconv.Itoa(replayFrames),
				},
			}, nil
		},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	firstDriver := New(cfg, log)
	first, err := firstDriver.Start(context.Background(), ports.ChatStartConfig{
		SessionID: "persistent-acp-e2e", DataDir: dataDir, WorkspacePath: workdir,
		ProviderScopeID: "scope", PrepareEnv: prepareEnv,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = nextEvent(t, first.Events()) // controller.ready
	ref, err := first.SendTurn(context.Background(), ports.ChatUserMessage{Text: "survive"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := first.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	var firstDelta ports.ChatEvent
	for firstDelta.Kind != ports.ChatEventMessageDelta {
		firstDelta = nextEvent(t, first.Events())
	}
	if !strings.Contains(firstDelta.Delta, "provider-pid=") {
		t.Fatalf("first provider delta = %#v", firstDelta)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("detach first daemon: %v", err)
	}

	// Simulate an app update moving/removing the new launch installation and
	// changed desired model/environment. None may prevent live adoption or
	// rewrite provider files/credentials.
	cfg.Launch = func(context.Context, LaunchConfig) (Launch, error) {
		return Launch{}, errors.New("new provider installation is unavailable")
	}
	secondDriver := New(cfg, log)
	second, err := secondDriver.Reconnect(context.Background(), ports.ChatResumeConfig{
		SessionID: "persistent-acp-e2e", DataDir: dataDir, WorkspacePath: workdir,
		ProviderConversationID: "persistent-provider-session", ProviderScopeID: "scope",
		PrepareEnv: prepareEnv, Model: "changed-model", Permissions: ports.PermissionModeAuto,
		Env: map[string]string{"PROVIDER_SETTING": "changed"}, SystemPrompt: "new application instructions",
	})
	if err != nil {
		t.Fatalf("Resume live host: %v", err)
	}
	defer func() { _ = second.(ports.ChatProviderTerminator).Terminate() }()
	if !second.(ports.ChatLiveReconnector).ReconnectedLive() {
		t.Fatal("replacement did not identify the same live ACP process")
	}
	if prepareCalls != 1 {
		t.Fatalf("launch-only environment prepared %d times, want only the initial provider launch", prepareCalls)
	}
	if err := second.(ports.ChatLiveReconnectActivator).ActivateLiveReconnect(context.Background(), ref.ProviderTurnID); err != nil {
		t.Fatalf("activate replacement: %v", err)
	}

	_ = nextEvent(t, second.Events()) // controller.ready reconstructed from cached setup
	if replayFrames > 0 {
		// Hold projection during the burst, just as a restarting daemon can be
		// behind the detached host. Neither connection nor history may be lost.
		time.Sleep(300 * time.Millisecond)
	}
	var replayedDelta, completed ports.ChatEvent
	deltas := 0
	replayTimeout := 5 * time.Second
	if replayFrames > 0 {
		replayTimeout = 30 * time.Second
	}
	deadline := time.After(replayTimeout)
	for completed.Kind != ports.ChatEventTurnCompleted {
		select {
		case event := <-second.Events():
			if event.Kind == ports.ChatEventMessageDelta {
				deltas++
				if event.ProviderEventID == firstDelta.ProviderEventID {
					replayedDelta = event
				}
			}
			if event.Kind == ports.ChatEventTurnCompleted {
				completed = event
			}
		case <-deadline:
			t.Fatal("timed out waiting for detached prompt completion")
		}
	}
	if deltas != replayFrames+1 {
		t.Fatalf("replayed %d deltas, want %d", deltas, replayFrames+1)
	}
	if replayedDelta.Delta != firstDelta.Delta || completed.ProviderTurnID != ref.ProviderTurnID ||
		completed.TurnState != domain.TurnStateCompleted || completed.ProviderEventID == "" {
		t.Fatalf("reconnect events: first=%#v replay=%#v completed=%#v", firstDelta, replayedDelta, completed)
	}
	if err := second.(ports.ChatProviderEventAcknowledger).AcknowledgeProviderEvent(context.Background(), completed.ProviderEventID); err != nil {
		t.Fatalf("acknowledge completion: %v", err)
	}

	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := New(cfg, log).Resume(context.Background(), ports.ChatResumeConfig{
		SessionID: "persistent-acp-e2e", DataDir: dataDir, WorkspacePath: workdir,
		ProviderConversationID: "persistent-provider-session", ProviderScopeID: "scope",
	})
	if err != nil {
		t.Fatalf("second restart: %v", err)
	}
	defer func() { _ = third.(ports.ChatProviderTerminator).Terminate() }()
	if err := third.(ports.ChatLiveReconnectActivator).ActivateLiveReconnect(context.Background(), ""); err != nil {
		t.Fatalf("completed prompt was not acknowledged before second restart: %v", err)
	}

	calls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"initialize", "session/new", "session/prompt"} {
		if got := strings.Count(string(calls), method+"\n"); got != 1 {
			t.Fatalf("provider method %s called %d times; calls:\n%s", method, got, calls)
		}
	}
}

// runPersistentACPTurn starts one turn and returns its terminal event once the
// conversation reports it can accept the next turn.
func runPersistentACPTurn(t *testing.T, conv ports.ChatConversation) ports.ChatEvent {
	t.Helper()
	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "turn"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	var terminal ports.ChatEvent
	for {
		event := nextEvent(t, conv.Events())
		if event.Kind == ports.ChatEventTurnCompleted && event.ProviderTurnID == ref.ProviderTurnID {
			terminal = event
		}
		if terminal.Kind != "" && event.Kind == ports.ChatEventControllerState &&
			event.ControllerState == ports.ChatControllerReady {
			return terminal
		}
	}
}

type fakeAgent struct {
	conn *acpsdk.AgentSideConnection

	mu                sync.Mutex
	initParams        acpsdk.InitializeRequest
	authMethods       []string
	capabilities      *acpsdk.AgentCapabilities
	initErr           error
	newParams         acpsdk.NewSessionRequest
	loadParams        acpsdk.LoadSessionRequest
	resumeParams      acpsdk.ResumeSessionRequest
	forkParams        acpsdk.UnstableForkSessionRequest
	forkID            string
	forkErr           error
	forkCalls         int
	onFork            func(context.Context) error
	deletedForks      []string
	loadUpdates       []acpsdk.SessionUpdate
	loadUpdateBatches [][]acpsdk.SessionUpdate
	blockLoadCall     int
	loadStarted       chan struct{}
	failLoadFrom      int   // LoadSession calls >= this number return failLoadErr
	failLoadErr       error // the SDK coerces a plain error into -32603
	// rejectReload answers a session/load of an already loaded, unclosed
	// session with Copilot's "already loaded" error.
	rejectReload        bool
	sessionOpen         bool
	closeCalls          int
	loadCalls           int
	resumeCalls         int
	promptParams        acpsdk.PromptRequest
	promptNoPermission  bool
	elicitation         *acpsdk.UnstableCreateElicitationRequest
	elicitationResponse acpsdk.UnstableCreateElicitationResponse
	promptErr           error
	promptResponse      *acpsdk.PromptResponse
	promptBlock         bool
	promptStarted       chan struct{}
	cancelErr           error
	cancelCalls         int
	customPrompt        func(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error)
	mode                string
	modeNotFound        bool // SetSessionMode returns -32601
	modeRequiresOption  SessionOption
	configNotFound      bool // SetSessionConfigOption returns -32601
	configErr           error
	newSessionUpdates   []acpsdk.SessionUpdate
	// newSessionHang, when set, holds session/new until it closes or the
	// request ends, like a provider still busy with a cold first start.
	newSessionHang chan struct{}
	options        map[string]string
	newConfig      []acpsdk.SessionConfigOption
	// lateConfig is sent as a config_option_update shortly after session/new answers.
	lateConfig          []acpsdk.SessionConfigOption
	setConfig           []acpsdk.SessionConfigOption
	setCalls            int
	steering            bool
	steerText           string
	steerPrompt         []acpsdk.ContentBlock
	steerMeta           map[string]any
	steerOut            string
	steerMethodNotFound bool // HandleExtensionMethod returns -32601 for steering
	compactSupported    bool // the bridge answers the Tallpine auto-compact request
	compactApplied      bool // ...and applies the window live
	compactCalls        []json.RawMessage
	stopTaskSupported   bool // the bridge answers the async-task stop request
	stopTaskStopped     bool
	stopTaskCalls       []json.RawMessage
	contextUsageReply   json.RawMessage // nil: the bridge has no context_usage request
	steerExtensionErr   error
}

type legacyKimiAgent struct {
	mu                 sync.Mutex
	currentModel       string
	availableModels    []legacyModelInfo
	rejectUnknownModel bool
	model              string
	modelCalls         int
	mode               string
	modeCalls          int
	configCalls        int
}

func fakeLegacyKimiSpawn(agent *legacyKimiAgent) spawnFunc {
	return func(Launch, string) (*process, error) {
		clientToAgentR, clientToAgentW := io.Pipe()
		agentToClientR, agentToClientW := io.Pipe()
		go serveLegacyKimi(agent, clientToAgentR, agentToClientW)
		return pipeProcess(clientToAgentR, clientToAgentW, agentToClientR, agentToClientW), nil
	}
}

func serveLegacyKimi(agent *legacyKimiAgent, in io.Reader, out io.Writer) {
	decoder := json.NewDecoder(in)
	encoder := json.NewEncoder(out)
	for {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := decoder.Decode(&request); err != nil {
			return
		}
		result := any(map[string]any{})
		var responseError any
		switch request.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": acpsdk.ProtocolVersionNumber,
				"agentCapabilities": map[string]any{
					"sessionCapabilities": map[string]any{"resume": map[string]any{}},
				},
			}
		case "session/new":
			currentModel := agent.currentModel
			availableModels := agent.availableModels
			if currentModel == "" {
				currentModel = "kimi-code/kimi-for-coding"
			}
			if len(availableModels) == 0 {
				availableModels = []legacyModelInfo{{
					ModelID: "kimi-code/kimi-for-coding", Name: "Kimi for Coding",
				}}
			}
			result = map[string]any{
				"sessionId": "kimi-session-1",
				"models": map[string]any{
					"currentModelId":  currentModel,
					"availableModels": availableModels,
				},
				"modes": map[string]any{
					"currentModeId": "default",
					"availableModes": []map[string]any{{
						"id": "default", "name": "Default", "description": "The default mode.",
					}},
				},
			}
		case "session/set_model":
			var params struct {
				ModelID string `json:"modelId"`
			}
			_ = json.Unmarshal(request.Params, &params)
			agent.mu.Lock()
			accepted := !agent.rejectUnknownModel
			for _, model := range agent.availableModels {
				accepted = accepted || model.ModelID == params.ModelID
			}
			if accepted {
				agent.model = params.ModelID
				agent.modelCalls++
			} else {
				responseError = map[string]any{
					"code": -32602, "message": "Invalid params",
					"data": map[string]any{"message": "Invalid model value: " + params.ModelID},
				}
			}
			agent.mu.Unlock()
		case "session/set_mode":
			var params struct {
				ModeID string `json:"modeId"`
			}
			_ = json.Unmarshal(request.Params, &params)
			agent.mu.Lock()
			agent.mode = params.ModeID
			agent.modeCalls++
			agent.mu.Unlock()
		case "session/set_config_option":
			agent.mu.Lock()
			agent.configCalls++
			agent.mu.Unlock()
			responseError = map[string]any{"code": -32601, "message": "Method not found"}
		default:
			responseError = map[string]any{"code": -32601, "message": "Method not found"}
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		if responseError != nil {
			response["error"] = responseError
		} else {
			response["result"] = result
		}
		if err := encoder.Encode(response); err != nil {
			return
		}
	}
}

var _ acpsdk.Agent = (*fakeAgent)(nil)

func (a *fakeAgent) Authenticate(_ context.Context, params acpsdk.AuthenticateRequest) (acpsdk.AuthenticateResponse, error) {
	a.mu.Lock()
	a.authMethods = append(a.authMethods, params.MethodId)
	a.mu.Unlock()
	return acpsdk.AuthenticateResponse{}, nil
}
func (a *fakeAgent) Initialize(_ context.Context, params acpsdk.InitializeRequest) (acpsdk.InitializeResponse, error) {
	a.mu.Lock()
	a.initParams = params
	initErr := a.initErr
	caps := a.capabilities
	a.mu.Unlock()
	if initErr != nil {
		return acpsdk.InitializeResponse{}, initErr
	}
	meta := map[string]any(nil)
	if a.steering {
		meta = map[string]any{"steering": map[string]any{"supported": true}}
	}
	defaultCaps := acpsdk.AgentCapabilities{
		SessionCapabilities: acpsdk.SessionCapabilities{Resume: &acpsdk.SessionResumeCapabilities{}},
	}
	if caps != nil {
		defaultCaps = *caps
	}
	return acpsdk.InitializeResponse{
		ProtocolVersion:   acpsdk.ProtocolVersionNumber,
		Meta:              meta,
		AgentCapabilities: defaultCaps,
	}, nil
}

func (a *fakeAgent) HandleExtensionMethod(_ context.Context, method string, raw json.RawMessage) (any, error) {
	if method == contextUsageMethod {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.contextUsageReply == nil {
			return nil, acpsdk.NewMethodNotFound(method)
		}
		return a.contextUsageReply, nil
	}
	if method == stopAsyncTaskMethod {
		a.mu.Lock()
		defer a.mu.Unlock()
		if !a.stopTaskSupported {
			return nil, acpsdk.NewMethodNotFound(method)
		}
		a.stopTaskCalls = append(a.stopTaskCalls, raw)
		return map[string]any{"stopped": a.stopTaskStopped}, nil
	}
	if method == setAutoCompactWindowMethod {
		a.mu.Lock()
		defer a.mu.Unlock()
		if !a.compactSupported {
			return nil, acpsdk.NewMethodNotFound(method)
		}
		a.compactCalls = append(a.compactCalls, raw)
		return map[string]any{"applied": a.compactApplied}, nil
	}
	if method != steeringMethod {
		return nil, acpsdk.NewMethodNotFound(method)
	}
	a.mu.Lock()
	methodNotFound := a.steerMethodNotFound
	extensionErr := a.steerExtensionErr
	a.mu.Unlock()
	if methodNotFound {
		return nil, acpsdk.NewMethodNotFound(method)
	}
	if extensionErr != nil {
		return nil, extensionErr
	}
	var params struct {
		Prompt []acpsdk.ContentBlock `json:"prompt"`
		Meta   map[string]any        `json:"_meta"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	text := ""
	if len(params.Prompt) > 0 && params.Prompt[0].Text != nil {
		text = params.Prompt[0].Text.Text
	}
	a.mu.Lock()
	a.steerText = text
	a.steerPrompt = append([]acpsdk.ContentBlock(nil), params.Prompt...)
	a.steerMeta = params.Meta
	outcome := a.steerOut
	a.mu.Unlock()
	if outcome == "" {
		outcome = "injected"
	}
	return steeringResponse{Outcome: outcome}, nil
}
func (a *fakeAgent) Logout(context.Context, acpsdk.LogoutRequest) (acpsdk.LogoutResponse, error) {
	return acpsdk.LogoutResponse{}, nil
}
func (a *fakeAgent) Cancel(context.Context, acpsdk.CancelNotification) error {
	a.mu.Lock()
	a.cancelCalls++
	err := a.cancelErr
	a.mu.Unlock()
	return err
}
func (a *fakeAgent) CloseSession(context.Context, acpsdk.CloseSessionRequest) (acpsdk.CloseSessionResponse, error) {
	a.mu.Lock()
	a.closeCalls++
	a.sessionOpen = false
	a.mu.Unlock()
	return acpsdk.CloseSessionResponse{}, nil
}
func (a *fakeAgent) ListSessions(context.Context, acpsdk.ListSessionsRequest) (acpsdk.ListSessionsResponse, error) {
	return acpsdk.ListSessionsResponse{}, nil
}
func (a *fakeAgent) NewSession(ctx context.Context, params acpsdk.NewSessionRequest) (acpsdk.NewSessionResponse, error) {
	a.mu.Lock()
	a.newParams = params
	updates := append([]acpsdk.SessionUpdate(nil), a.newSessionUpdates...)
	hang := a.newSessionHang
	a.mu.Unlock()
	if hang != nil {
		select {
		case <-hang:
		case <-ctx.Done():
		}
		return acpsdk.NewSessionResponse{}, errors.New("session/new abandoned")
	}
	for _, update := range updates {
		if err := a.conn.SessionUpdate(ctx, acpsdk.SessionNotification{SessionId: "claude-session-1", Update: update}); err != nil {
			return acpsdk.NewSessionResponse{}, err
		}
	}
	if late := a.lateConfig; len(late) > 0 {
		go func() {
			time.Sleep(150 * time.Millisecond)
			_ = a.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
				SessionId: "claude-session-1",
				Update:    acpsdk.SessionUpdate{ConfigOptionUpdate: &acpsdk.SessionConfigOptionUpdate{ConfigOptions: late}},
			})
		}()
	}
	return acpsdk.NewSessionResponse{SessionId: "claude-session-1", ConfigOptions: a.newConfig}, nil
}
func (a *fakeAgent) ResumeSession(_ context.Context, params acpsdk.ResumeSessionRequest) (acpsdk.ResumeSessionResponse, error) {
	a.mu.Lock()
	a.resumeParams = params
	a.resumeCalls++
	a.mu.Unlock()
	return acpsdk.ResumeSessionResponse{}, nil
}
func (a *fakeAgent) UnstableForkSession(ctx context.Context, params acpsdk.UnstableForkSessionRequest) (acpsdk.UnstableForkSessionResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.forkParams = params
	a.forkCalls++
	if a.onFork != nil {
		if err := a.onFork(ctx); err != nil {
			return acpsdk.UnstableForkSessionResponse{}, err
		}
	}
	return acpsdk.UnstableForkSessionResponse{SessionId: acpsdk.SessionId(a.forkID)}, a.forkErr
}
func (a *fakeAgent) UnstableDeleteSession(_ context.Context, params acpsdk.UnstableDeleteSessionRequest) (acpsdk.UnstableDeleteSessionResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.deletedForks = append(a.deletedForks, string(params.SessionId))
	return acpsdk.UnstableDeleteSessionResponse{}, nil
}
func (a *fakeAgent) LoadSession(ctx context.Context, params acpsdk.LoadSessionRequest) (acpsdk.LoadSessionResponse, error) {
	a.mu.Lock()
	a.loadParams = params
	a.loadCalls++
	loadCall := a.loadCalls
	updates := append([]acpsdk.SessionUpdate(nil), a.loadUpdates...)
	if batch := a.loadCalls - 1; batch < len(a.loadUpdateBatches) {
		updates = append([]acpsdk.SessionUpdate(nil), a.loadUpdateBatches[batch]...)
	}
	block := a.blockLoadCall == loadCall
	started := a.loadStarted
	failLoad := a.failLoadFrom > 0 && loadCall >= a.failLoadFrom
	failLoadErr := a.failLoadErr
	if a.rejectReload && a.sessionOpen {
		a.mu.Unlock()
		return acpsdk.LoadSessionResponse{}, acpsdk.NewInvalidParams("Session " + string(params.SessionId) + " is already loaded")
	}
	a.sessionOpen = true
	a.mu.Unlock()
	if failLoad {
		return acpsdk.LoadSessionResponse{}, failLoadErr
	}
	if block {
		if started != nil {
			close(started)
		}
		<-ctx.Done()
		return acpsdk.LoadSessionResponse{}, ctx.Err()
	}
	for _, update := range updates {
		if err := a.conn.SessionUpdate(ctx, acpsdk.SessionNotification{SessionId: params.SessionId, Update: update}); err != nil {
			return acpsdk.LoadSessionResponse{}, err
		}
	}
	return acpsdk.LoadSessionResponse{}, nil
}
func (a *fakeAgent) SetSessionConfigOption(_ context.Context, params acpsdk.SetSessionConfigOptionRequest) (acpsdk.SetSessionConfigOptionResponse, error) {
	a.mu.Lock()
	a.setCalls++
	if a.configErr != nil {
		err := a.configErr
		a.mu.Unlock()
		return acpsdk.SetSessionConfigOptionResponse{}, err
	}
	if a.configNotFound {
		a.mu.Unlock()
		return acpsdk.SetSessionConfigOptionResponse{}, acpsdk.NewMethodNotFound("session/set_config_option")
	}
	if params.ValueId != nil {
		if a.options == nil {
			a.options = make(map[string]string)
		}
		a.options[string(params.ValueId.ConfigId)] = string(params.ValueId.Value)
	}
	if params.Boolean != nil {
		if a.options == nil {
			a.options = make(map[string]string)
		}
		a.options[string(params.Boolean.ConfigId)] = fmt.Sprintf("%t", params.Boolean.Value)
	}
	response := append([]acpsdk.SessionConfigOption(nil), a.setConfig...)
	a.mu.Unlock()
	return acpsdk.SetSessionConfigOptionResponse{ConfigOptions: response}, nil
}
func (a *fakeAgent) SetSessionMode(_ context.Context, params acpsdk.SetSessionModeRequest) (acpsdk.SetSessionModeResponse, error) {
	a.mu.Lock()
	if required := a.modeRequiresOption; required.ID != "" && a.options[required.ID] != required.Value {
		a.mu.Unlock()
		return acpsdk.SetSessionModeResponse{}, acpsdk.NewInternalError(map[string]any{
			"details": "Mode auto is not available in this session",
		})
	}
	if a.modeNotFound {
		a.mu.Unlock()
		return acpsdk.SetSessionModeResponse{}, acpsdk.NewMethodNotFound("session/set_mode")
	}
	a.mode = string(params.ModeId)
	a.mu.Unlock()
	return acpsdk.SetSessionModeResponse{}, nil
}
func (a *fakeAgent) Prompt(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
	a.mu.Lock()
	a.promptParams = params
	promptNoPermission := a.promptNoPermission
	elicitation := a.elicitation
	promptErr := a.promptErr
	promptResponse := a.promptResponse
	promptBlock := a.promptBlock
	promptStarted := a.promptStarted
	customPrompt := a.customPrompt
	a.mu.Unlock()
	if customPrompt != nil {
		return customPrompt(ctx, params)
	}
	if promptErr != nil {
		return acpsdk.PromptResponse{}, promptErr
	}
	if promptResponse != nil {
		return *promptResponse, nil
	}
	if promptBlock {
		if promptStarted != nil {
			select {
			case promptStarted <- struct{}{}:
			default:
			}
		}
		<-ctx.Done()
		return acpsdk.PromptResponse{}, ctx.Err()
	}
	if elicitation != nil {
		response, err := a.conn.UnstableCreateElicitation(ctx, *elicitation)
		a.mu.Lock()
		a.elicitationResponse = response
		a.mu.Unlock()
		if err != nil {
			return acpsdk.PromptResponse{}, err
		}
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	if promptNoPermission {
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	_ = a.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
		SessionId: params.SessionId,
		Update:    acpsdk.UpdateAgentMessageText("working"),
	})
	permission, err := a.conn.RequestPermission(ctx, acpsdk.RequestPermissionRequest{
		SessionId: params.SessionId,
		ToolCall: acpsdk.ToolCallUpdate{
			ToolCallId: "tool-1", Title: acpsdk.Ptr("Edit file"), Kind: acpsdk.Ptr(acpsdk.ToolKindEdit),
		},
		Options: []acpsdk.PermissionOption{
			{OptionId: "allow", Name: "Allow", Kind: acpsdk.PermissionOptionKindAllowOnce},
			{OptionId: "reject", Name: "Reject", Kind: acpsdk.PermissionOptionKindRejectOnce},
		},
	})
	if err != nil {
		return acpsdk.PromptResponse{}, err
	}
	if permission.Outcome.Selected != nil {
		_ = a.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
			SessionId: params.SessionId,
			Update:    acpsdk.UpdateAgentMessageText(" done"),
		})
	}
	return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
}

func delayedCancelSpawn(
	promptReceived chan<- struct{},
	cancelReceived chan<- struct{},
	completePrompt <-chan struct{},
	responseSent chan<- error,
) spawnFunc {
	return func(Launch, string) (*process, error) {
		clientToAgentR, clientToAgentW := io.Pipe()
		agentToClientR, agentToClientW := io.Pipe()
		go func() {
			decoder := json.NewDecoder(clientToAgentR)
			encoder := json.NewEncoder(agentToClientW)
			var promptID json.RawMessage
			for {
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				if err := decoder.Decode(&request); err != nil {
					return
				}
				switch request.Method {
				case "initialize":
					_ = encoder.Encode(map[string]any{
						"jsonrpc": "2.0", "id": request.ID,
						"result": map[string]any{
							"protocolVersion":   acpsdk.ProtocolVersionNumber,
							"agentCapabilities": map[string]any{},
							"authMethods":       []any{},
						},
					})
				case "session/new":
					_ = encoder.Encode(map[string]any{
						"jsonrpc": "2.0", "id": request.ID,
						"result": map[string]any{"sessionId": "cancel-session"},
					})
				case "session/prompt":
					promptID = append(json.RawMessage(nil), request.ID...)
					select {
					case promptReceived <- struct{}{}:
					default:
					}
				case "session/cancel":
					select {
					case cancelReceived <- struct{}{}:
					default:
					}
					<-completePrompt
					responseSent <- encoder.Encode(map[string]any{
						"jsonrpc": "2.0", "id": promptID,
						"result": map[string]any{"stopReason": "cancelled"},
					})
				case "session/close":
					_ = encoder.Encode(map[string]any{
						"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{},
					})
				}
			}
		}()
		return pipeProcess(clientToAgentR, clientToAgentW, agentToClientR, agentToClientW), nil
	}
}

// standingInstructions is the fixed preamble text used by the preamble tests.
const standingInstructions = "Tallpine standing instructions for this session"

// runOneTurn sends a single text turn and returns the prompt the provider saw.
// The caller must already have drained the controller.ready event emitted at
// session setup.
func runOneTurn(t *testing.T, agent *fakeAgent, conv ports.ChatConversation, text string) []acpsdk.ContentBlock {
	t.Helper()
	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{
		Text: text, ClientMessageID: "tallpine-client-message-preamble",
	})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	// The turn is released only after it completes, just before the driver
	// announces it is ready; wait for that, as a caller sending the next turn
	// must, or the next SendTurn can find the turn still in flight.
	completed := false
	for {
		event := nextEvent(t, conv.Events())
		if event.Kind == ports.ChatEventTurnCompleted {
			completed = true
		}
		if completed && event.Kind == ports.ChatEventControllerState && event.ControllerState == ports.ChatControllerReady {
			break
		}
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return append([]acpsdk.ContentBlock(nil), agent.promptParams.Prompt...)
}

// preambleText reads Tallpine's standing instructions back out of a native prompt the
// way the agent does: the resource block is what reaches the model.
func preambleText(prompt []acpsdk.ContentBlock) string {
	if len(prompt) == 0 || prompt[0].Resource == nil {
		return ""
	}
	contents := prompt[0].Resource.Resource.TextResourceContents
	if contents == nil {
		return ""
	}
	return contents.Text
}

// preambleConfig is the binding shape under test: an agent with no
// system-prompt launch flag that ignores session/new _meta and receives Tallpine's
// standing context only as an embedded resource on the first prompt.
func preambleConfig() Config {
	cfg := fakeConfig()
	cfg.SessionPreamble = func(cfg LaunchConfig) []acpsdk.ContentBlock {
		return []acpsdk.ContentBlock{acpsdk.ResourceBlock(acpsdk.EmbeddedResourceResource{
			TextResourceContents: &acpsdk.TextResourceContents{
				Uri:  "tallpine://tallpine/standing-instructions",
				Text: cfg.SystemPrompt,
			},
		})}
	}
	return cfg
}

func awaitSkillCount(t *testing.T, lister ports.ChatSkillLister, want int) []ports.ChatSkill {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		skills, err := lister.ListSkills(context.Background())
		if err != nil {
			t.Fatalf("ListSkills: %v", err)
		}
		if len(skills) == want {
			return skills
		}
		if time.Now().After(deadline) {
			t.Fatalf("skills = %#v, want %d", skills, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func selectConfigOption(id, name, category, current string, values ...string) acpsdk.SessionConfigOption {
	categoryValue := acpsdk.SessionConfigOptionCategory(category)
	choices := make(acpsdk.SessionConfigSelectOptionsUngrouped, 0, len(values))
	for _, value := range values {
		choices = append(choices, acpsdk.SessionConfigSelectOption{
			Value: acpsdk.SessionConfigValueId(value), Name: value,
		})
	}
	return acpsdk.SessionConfigOption{Select: &acpsdk.SessionConfigOptionSelect{
		Id: acpsdk.SessionConfigId(id), Name: name, Category: &categoryValue,
		CurrentValue: acpsdk.SessionConfigValueId(current),
		Options:      acpsdk.SessionConfigSelectOptions{Ungrouped: &choices},
		Type:         "select",
	}}
}

func booleanConfigOption(id, name string, current bool) acpsdk.SessionConfigOption {
	return acpsdk.SessionConfigOption{Boolean: &acpsdk.SessionConfigOptionBoolean{
		Id: acpsdk.SessionConfigId(id), Name: name, CurrentValue: current, Type: "boolean",
	}}
}

func fakeSpawn(agent *fakeAgent) spawnFunc {
	return func(Launch, string) (*process, error) {
		clientToAgentR, clientToAgentW := io.Pipe()
		agentToClientR, agentToClientW := io.Pipe()
		agent.conn = acpsdk.NewAgentSideConnection(agent, agentToClientW, clientToAgentR)
		return pipeProcess(clientToAgentR, clientToAgentW, agentToClientR, agentToClientW), nil
	}
}

func nextEvent(t *testing.T, events <-chan ports.ChatEvent) ports.ChatEvent {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("event stream closed")
		}
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
		return ports.ChatEvent{}
	}
}

// Tests substitute provider stdio at the same private seam as real host
// connections. Catalog discovery shares only the ephemeral spawn dependency.
func (d *Driver) useTestProcess(spawn spawnFunc) {
	d.spawn = spawn
	d.openProcess = func(ctx context.Context, cfg LaunchConfig, prepare func(context.Context) (map[string]string, error)) (*process, error) {
		if prepare != nil {
			env, err := prepare(ctx)
			if err != nil {
				return nil, err
			}
			cfg.Env = env
		}
		launch, err := d.cfg.Launch(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return spawn(launch, cfg.WorkspacePath)
	}
}
