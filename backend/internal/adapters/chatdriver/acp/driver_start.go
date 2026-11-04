package acp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Start creates a new ACP session in the Tallpine worktree.
func (d *Driver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	totalStarted := time.Now()
	if !filepath.IsAbs(cfg.WorkspacePath) {
		return nil, fmt.Errorf("workspace path must be absolute, got %q", cfg.WorkspacePath)
	}
	if d.cfg.ValidateTurnSettings != nil {
		if err := d.cfg.ValidateTurnSettings(cfg.Permissions, ports.ChatTurnSettings{
			Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions,
		}); err != nil {
			return nil, fmt.Errorf("validate ACP session settings: %w", err)
		}
	}
	launchCfg := LaunchConfig{
		Policy:    cfg.Policy,
		SessionID: cfg.SessionID, DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath,
		Env:   cfg.Env,
		Model: cfg.Model, Permissions: cfg.Permissions, SystemPrompt: cfg.SystemPrompt,
		DisableTools:    cfg.DisableTools,
		ProviderScopeID: cfg.ProviderScopeID,
	}
	settings := ports.ChatTurnSettings{Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions}
	for attempt := 1; ; attempt++ {
		conv, err := d.startOnce(ctx, cfg, launchCfg, settings)
		if err == nil {
			d.logStartStage(cfg.SessionID, "total", totalStarted, nil)
			return conv, nil
		}
		if !errors.Is(err, errSessionNewTimeout) || attempt > len(sessionNewRetryDelays) {
			return nil, err
		}
		// A cold provider (the first start after install or app launch) can miss
		// the session/new deadline once and then answer in under a second. Start
		// a fresh process rather than wait on the one that stalled.
		delay := sessionNewRetryDelays[attempt-1]
		d.log.Warn("chat: ACP session/new timed out; starting the agent again",
			"sessionID", cfg.SessionID, "harness", d.cfg.Harness, "attempt", attempt, "retryIn", delay)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, err
		}
	}
}

// startOnce launches the agent and opens a new provider session on it.
func (d *Driver) startOnce(
	ctx context.Context,
	cfg ports.ChatStartConfig,
	launchCfg LaunchConfig,
	settings ports.ChatTurnSettings,
) (*conversation, error) {
	connectStarted := time.Now()
	conv, init, live, err := d.connect(ctx, launchCfg, cfg.PrepareEnv)
	d.logStartStage(cfg.SessionID, "connect", connectStarted, err)
	if err != nil {
		return nil, err
	}
	if live != nil {
		// A fresh-start request colliding with a surviving host is a durable-state
		// mismatch. Detach without destroying a provider that may still be working.
		conv.discard()
		return nil, fmt.Errorf("%w: fresh ACP start found an existing initialized session", ports.ErrChatRecoveryInconclusive)
	}
	// An agent that does not advertise session/resume can still start new
	// sessions; it just cannot be resumed after a disconnect. The resume
	// capability is downgraded in conversationCapabilities, and Resume()
	// returns ErrChatResumeFailed if called.
	additional, err := normalizeAdditionalDirectories(cfg.WorkspacePath, cfg.AdditionalDirectories,
		init.AgentCapabilities.SessionCapabilities.AdditionalDirectories != nil)
	if err != nil {
		conv.discard()
		return nil, err
	}
	mcpServers, err := normalizeMCPServers(cfg.MCPServers, init.AgentCapabilities.McpCapabilities)
	if err != nil {
		conv.discard()
		return nil, err
	}
	if err := d.openNewSession(ctx, conv, init, launchCfg, settings, additional, mcpServers); err != nil {
		conv.discard()
		return nil, err
	}
	return conv, nil
}

// errSessionNewTimeout marks a session/new that ran out its own deadline while
// the caller was still waiting. Start retries it on a fresh process.
var errSessionNewTimeout = errors.New("ACP session/new timed out")

// sessionNewRetryDelays are the pauses before each extra start attempt after a
// session/new timeout; its length is the number of retries. Every attempt has
// its own sessionNewTimeout, so the worst case stays inside the async spawn
// budget.
var sessionNewRetryDelays = []time.Duration{2 * time.Second, 5 * time.Second}

// sessionNewTimeout bounds one session/new request. A variable so tests can
// exercise the retry without waiting a minute.
var sessionNewTimeout = handshakeTimeout

// openNewSession creates a provider session on an initialized connection and
// applies the requested turn settings. The caller discards conv on error.
func (d *Driver) openNewSession(
	ctx context.Context,
	conv *conversation,
	init acpsdk.InitializeResponse,
	launchCfg LaunchConfig,
	settings ports.ChatTurnSettings,
	additional []string,
	mcpServers []acpsdk.McpServer,
) error {
	meta := map[string]any(nil)
	if d.cfg.SessionMeta != nil {
		meta = d.cfg.SessionMeta(launchCfg)
	}
	openCtx, cancel := context.WithTimeout(ctx, sessionNewTimeout)
	defer cancel()
	sessionStarted := time.Now()
	resp, err := conv.conn.NewSession(openCtx, acpsdk.NewSessionRequest{
		Meta:                  meta,
		Cwd:                   launchCfg.WorkspacePath,
		AdditionalDirectories: additional,
		McpServers:            mcpServers,
	})
	d.logStartStage(launchCfg.SessionID, "session_new", sessionStarted, err)
	if err != nil {
		if ctx.Err() == nil && errors.Is(openCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w after %s: %w", errSessionNewTimeout, sessionNewTimeout, err)
		}
		return normalizeACPError("ACP session/new", err)
	}
	if resp.SessionId == "" {
		return errors.New("ACP session/new returned no session id")
	}
	conv.start(
		string(resp.SessionId), conversationCapabilities(d.cfg.Capabilities, init),
		d.sessionModeFor(launchCfg.Policy), d.cfg.SessionOptions, d.permissionPolicyFor(launchCfg.Policy),
		settings.Approval, d.cfg.ValidateTurnSettings, resp.ConfigOptions,
		conv.legacyWire.modelState(), resp.Modes,
	)
	if d.cfg.EncodeProviderConversationID != nil {
		conv.setReportedProviderConversationID(d.cfg.EncodeProviderConversationID(string(resp.SessionId)))
	}
	conv.setSessionPreamble(d.sessionPreamble(launchCfg))
	if err := d.applyLaunchSessionOptions(ctx, conv, launchCfg); err != nil {
		return fmt.Errorf("configure ACP session: %w", err)
	}
	settingsStarted := time.Now()
	err = conv.applyTurnSettings(ctx, settings)
	d.logStartStage(launchCfg.SessionID, "initial_settings", settingsStarted, err)
	if err != nil {
		// Initial model and permission mode may have been applied via launch-time
		// flags (e.g. kimchiacp passes --model, --auto, --yolo). An agent that
		// does not implement the runtime ACP setters returns -32601; tolerate it
		// at session start so those bindings can still open a session.
		if !errors.Is(err, ErrACPSetterUnsupported) {
			return fmt.Errorf("configure ACP session: %w", err)
		}
	}
	return nil
}

// applyLaunchSessionOptions sets the binding's session-scoped config options.
// A persistent-host reconnect skips it: that provider session already has them.
func (d *Driver) applyLaunchSessionOptions(ctx context.Context, conv *conversation, cfg LaunchConfig) error {
	if d.cfg.LaunchSessionOptions == nil {
		return nil
	}
	return conv.applySessionOptions(ctx, d.cfg.LaunchSessionOptions(cfg))
}

func (d *Driver) sessionPreamble(cfg LaunchConfig) []acpsdk.ContentBlock {
	if d.cfg.SessionPreamble == nil {
		return nil
	}
	return d.cfg.SessionPreamble(cfg)
}

func (d *Driver) logStartStage(sessionID domain.SessionID, stage string, started time.Time, err error) {
	attrs := []any{
		"sessionID", sessionID,
		"harness", d.cfg.Harness,
		"stage", stage,
		"duration", time.Since(started),
		"outcome", "ok",
	}
	if err != nil {
		// The cause is the only evidence of a provider-side hang or refusal;
		// without it a failed resume cannot be told apart from a timeout.
		attrs[len(attrs)-1] = "error"
		attrs = append(attrs, "err", err.Error())
	}
	d.log.Info("chat: ACP start stage", attrs...)
}
