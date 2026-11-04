package acp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/processenv"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (d *Driver) connect(
	ctx context.Context,
	cfg LaunchConfig,
	prepareEnv func(context.Context) (map[string]string, error),
) (*conversation, acpsdk.InitializeResponse, *persistenthost.ACPState, error) {
	if cfg.ReconnectOnly && (cfg.DataDir == "" || cfg.SessionID == "") {
		return nil, acpsdk.InitializeResponse{}, nil, ports.ErrChatHostNotRunning
	}
	processStarted := time.Now()
	proc, err := d.openProcess(ctx, cfg, prepareEnv)
	d.logStartStage(cfg.SessionID, "process_open", processStarted, err)
	if err != nil {
		return nil, acpsdk.InitializeResponse{}, nil, err
	}
	initializeStarted := time.Now()
	conv, init, state, err := d.initialize(ctx, cfg, proc)
	d.logStartStage(cfg.SessionID, "initialize", initializeStarted, err)
	reopen := func(stage string) (*conversation, acpsdk.InitializeResponse, *persistenthost.ACPState, error) {
		proc, err := d.openProcess(ctx, cfg, prepareEnv)
		if err != nil {
			return nil, acpsdk.InitializeResponse{}, nil, err
		}
		started := time.Now()
		conv, init, state, err := d.initialize(ctx, cfg, proc)
		d.logStartStage(cfg.SessionID, stage, started, err)
		return conv, init, state, err
	}
	if errors.Is(err, errHostNeverStarted) {
		// Nothing to recover: stop the host so it does not block every later
		// start, then start the agent afresh, which reloads the conversation.
		d.log.Info("chat: ACP host never finished starting; replacing it", "sessionID", cfg.SessionID, "harness", d.cfg.Harness)
		if stopErr := proc.terminate(); stopErr != nil {
			return nil, acpsdk.InitializeResponse{}, nil, fmt.Errorf(
				"%w: stop ACP host that never finished starting: %w", ports.ErrChatRecoveryInconclusive, stopErr)
		}
		if cfg.ReconnectOnly {
			return nil, acpsdk.InitializeResponse{}, nil, ports.ErrChatHostNotRunning
		}
		return reopen("initialize_replaced")
	}
	if err == nil || !d.cfg.RetryStartupExit || proc.reconnected || !isACPPeerDisconnected(err) {
		return conv, init, state, err
	}
	// Some agents exit during a cold first handshake (pi-acp while its
	// background daemon comes up); a second start finds it running.
	d.log.Info("chat: ACP agent exited during its first handshake; starting it once more",
		"sessionID", cfg.SessionID, "harness", d.cfg.Harness, "error", err)
	select {
	case <-time.After(startupRetryDelay):
	case <-ctx.Done():
		return nil, acpsdk.InitializeResponse{}, nil, err
	}
	return reopen("initialize_retry")
}

// errHostNeverStarted is a reattached persistent host that holds no session.
var errHostNeverStarted = errors.New("persistent ACP host never finished starting")

// startupRetryDelay lets an exited agent release its host before the retry.
const startupRetryDelay = 500 * time.Millisecond

// isACPPeerDisconnected reports a request unanswered because the agent exited.
func isACPPeerDisconnected(err error) bool {
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) {
		return false
	}
	data, _ := requestErr.Data.(map[string]any)
	reason, _ := data["error"].(string)
	// The handshake can also fail writing to the gone process's input.
	for _, gone := range []string{"peer disconnected", "io: read/write on closed pipe", "broken pipe", "file already closed"} {
		if strings.Contains(reason, gone) {
			return true
		}
	}
	return false
}

func (d *Driver) initialize(
	ctx context.Context,
	cfg LaunchConfig,
	proc *process,
) (*conversation, acpsdk.InitializeResponse, *persistenthost.ACPState, error) {
	conv := newConversation(
		proc, d.log, cfg.ProviderScopeID, d.cfg.ClientExtension, d.cfg.ClientExtensionAliases,
	)
	if onRejected := d.cfg.OnAuthRejected; onRejected != nil {
		conv.onAuthRejected = func() { onRejected(cfg) }
	}
	conv.promptResponseFailure = d.cfg.PromptResponseFailure
	conv.turnUsage = d.cfg.TurnUsage
	conv.usageRateLimits = d.cfg.UsageRateLimits
	conv.infoContextUsage = d.cfg.InfoContextUsage
	conv.sessionPromptResponseFailure = d.cfg.SessionPromptResponseFailure
	conv.sessionUsage = d.cfg.SessionUsage
	conv.hibernationCheck = d.cfg.CanHibernate
	conv.decodeConversationID = d.cfg.DecodeProviderConversationID
	conv.historyNotice = d.cfg.HistoryNotice
	conv.historyUserChunkIsMessage = d.cfg.HistoryUserChunkIsMessage
	conv.plansSpanTurns = d.cfg.PlansSpanTurns
	conv.requireAdvertised = d.cfg.RequireAdvertisedOptions
	conv.optionCatalogWait = d.cfg.OptionCatalogWait
	if proc.reconnected {
		state := proc.acpState
		if state == nil || state.SessionID == "" {
			// The host's first handshake was cut short (a daemon stopped mid-start).
			conv.discard()
			return nil, acpsdk.InitializeResponse{}, nil, errHostNeverStarted
		}
		if len(state.InitializeResult) == 0 || len(state.SessionResult) == 0 {
			conv.discard()
			return nil, acpsdk.InitializeResponse{}, nil, fmt.Errorf(
				"%w: persistent ACP host has incomplete reconnect state", ports.ErrChatRecoveryInconclusive)
		}
		var init acpsdk.InitializeResponse
		if err := json.Unmarshal(state.InitializeResult, &init); err != nil {
			conv.discard()
			return nil, acpsdk.InitializeResponse{}, nil, fmt.Errorf(
				"%w: decode persistent ACP initialize: %w", ports.ErrChatRecoveryInconclusive, err)
		}
		if d.cfg.ValidateInitialize != nil {
			if err := d.cfg.ValidateInitialize(init); err != nil {
				conv.discard()
				return nil, acpsdk.InitializeResponse{}, nil, fmt.Errorf("%w: %w", ports.ErrChatDriverIncompatible, err)
			}
		}
		conv.canDeleteFork = init.AgentCapabilities.SessionCapabilities.Delete != nil
		return conv, init, state, nil
	}

	initCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	init, err := conv.conn.Initialize(initCtx, acpsdk.InitializeRequest{
		Meta: map[string]any{
			persistenthost.ACPInitialPermissionsMetaKey: string(ports.NormalizePermissionMode(cfg.Permissions)),
		},
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientInfo: &acpsdk.Implementation{
			Name:    "tallpine",
			Title:   pointer("Tallpine"),
			Version: "0.1.0",
		},
		ClientCapabilities: acpsdk.ClientCapabilities{
			// These two Claude bridge extensions enrich the transcript. They do
			// not grant the agent access to Tallpine's terminal or filesystem APIs.
			Meta: map[string]any{
				// Cursor reports a model's effort, thinking and speed as their own
				// settings only to a client that asks. Other agents ignore it.
				"parameterizedModelPicker": true,
				"subagent-transcript":      true,
				"terminal_output":          true,
				// claude-agent-acp publishes retryable API/transport failures only
				// when the client opts into this namespaced metadata extension. It is
				// observational: Tallpine receives status, but grants no new capability.
				"jetbrains": map[string]any{
					"air": map[string]any{
						"version":      1,
						"capabilities": []string{"sessionFailure", "asyncTasks"},
					},
				},
			},
			Elicitation: &acpsdk.ElicitationCapabilities{
				Form: &acpsdk.ElicitationFormCapabilities{},
				Url:  &acpsdk.ElicitationUrlCapabilities{},
			},
			PlanCapabilities: &acpsdk.PlanCapabilities{},
		},
	})
	if err != nil {
		conv.discard()
		if isACPAuthRequired(err) {
			return nil, acpsdk.InitializeResponse{}, nil, normalizeACPError("ACP initialize", err)
		}
		return nil, acpsdk.InitializeResponse{}, nil, fmt.Errorf("%w: ACP initialize: %w", ports.ErrChatDriverIncompatible, err)
	}
	if d.cfg.ValidateInitialize != nil {
		if err := d.cfg.ValidateInitialize(init); err != nil {
			conv.discard()
			return nil, acpsdk.InitializeResponse{}, nil, fmt.Errorf("%w: %w", ports.ErrChatDriverIncompatible, err)
		}
	}
	conv.canDeleteFork = init.AgentCapabilities.SessionCapabilities.Delete != nil
	if d.cfg.AfterInitialize != nil {
		if err := d.cfg.AfterInitialize(ctx, conv.conn, init, cfg); err != nil {
			conv.discard()
			return nil, acpsdk.InitializeResponse{}, nil, normalizeACPError("ACP after initialize", err)
		}
	}
	return conv, init, nil, nil
}

func (d *Driver) connectProcess(
	ctx context.Context,
	cfg LaunchConfig,
	prepareEnv func(context.Context) (map[string]string, error),
) (*process, error) {
	// Ownership is stable across app upgrades and mutable model/approval choices.
	// An adopted provider keeps its original launch environment and instructions;
	// changes to those take effect only when the provider is explicitly replaced.
	owner, _ := json.Marshal([]string{filepath.Clean(cfg.WorkspacePath), string(d.cfg.Harness), cfg.ProviderScopeID})
	identity := fmt.Sprintf("%x", sha256.Sum256(owner))
	hostConfig := persistenthost.Config{
		SessionID: string(cfg.SessionID), DataDir: cfg.DataDir, Workdir: cfg.WorkspacePath,
		Protocol: persistenthost.ProtocolACP, OwnershipFingerprint: identity,
		ReconnectOnly: cfg.ReconnectOnly,
		Prepare: func(prepareCtx context.Context) (persistenthost.PreparedProvider, error) {
			if prepareEnv != nil {
				env, err := prepareEnv(prepareCtx)
				if err != nil {
					return persistenthost.PreparedProvider{}, err
				}
				cfg.Env = env
			}
			// Binary resolution, generated provider files, and credential rotation
			// are launch-only effects. None run when reconnecting to a live host.
			launch, err := d.cfg.Launch(prepareCtx, cfg)
			if err != nil {
				return persistenthost.PreparedProvider{}, err
			}
			if launch.Command == "" {
				return persistenthost.PreparedProvider{}, fmt.Errorf("%w: ACP launch command is empty", ports.ErrChatDriverUnavailable)
			}
			return persistenthost.PreparedProvider{
				Env:  processenv.Merge(launch.Env),
				Argv: append([]string{launch.Command}, launch.Args...),
			}, nil
		},
	}
	transport, err := d.connectHost(ctx, hostConfig)
	if err != nil {
		if errors.Is(err, persistenthost.ErrNotRunning) {
			return nil, ports.ErrChatHostNotRunning
		}
		if errors.Is(err, persistenthost.ErrOwnershipInconclusive) ||
			errors.Is(err, persistenthost.ErrAttached) ||
			errors.Is(err, persistenthost.ErrIncompatible) ||
			errors.Is(err, persistenthost.ErrUnauthorized) {
			return nil, fmt.Errorf("%w: persistent ACP host: %w", ports.ErrChatRecoveryInconclusive, err)
		}
		return nil, fmt.Errorf("%w: persistent ACP host: %w", ports.ErrChatDriverUnavailable, err)
	}
	gate := newGatedReader(transport.Stdout, !transport.Reconnected)
	proc := &process{
		stdin: transport.Stdin, stdout: gate, gate: gate,
		reconnected: transport.Reconnected, acpState: transport.ACPState,
	}
	proc.stop = persistentStop(gate, transport.Stdin)
	if d.cfg.QuickStop {
		proc.quickStop.Store(true)
	}
	proc.terminate = persistentTerminate(gate, transport.Stdin, &proc.quickStop, func(shutdownCtx context.Context, quick bool) error {
		if quick {
			return persistenthost.ShutdownQuick(shutdownCtx, cfg.DataDir, string(cfg.SessionID))
		}
		return persistenthost.Shutdown(shutdownCtx, cfg.DataDir, string(cfg.SessionID))
	})
	return proc, nil
}
