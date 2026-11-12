package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// Reconnect attaches to a surviving provider without launching a replacement.
func (d *Driver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	cfg.ReconnectOnly = true
	return d.Resume(ctx, cfg)
}

// Resume reconnects to the stored ACP session. When the agent advertises
// session/load, Tallpine uses it to recover both provider context and the normalized
// transcript; resume-only agents recover context but explicitly report that no
// typed history replay is available.
func (d *Driver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if cfg.ProviderConversationID == "" {
		return nil, fmt.Errorf("%w: no stored ACP session id", ports.ErrChatResumeFailed)
	}
	reportedProviderConversationID := cfg.ProviderConversationID
	if d.cfg.DecodeProviderConversationID != nil {
		providerConversationID, err := d.cfg.DecodeProviderConversationID(cfg.ProviderConversationID)
		if err != nil {
			return nil, err
		}
		cfg.ProviderConversationID = providerConversationID
	}
	if !filepath.IsAbs(cfg.WorkspacePath) {
		return nil, fmt.Errorf("workspace path must be absolute, got %q", cfg.WorkspacePath)
	}
	if d.cfg.ValidateTurnSettings != nil {
		if err := d.cfg.ValidateTurnSettings(cfg.Permissions, ports.ChatTurnSettings{
			Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions,
		}); err != nil {
			return nil, fmt.Errorf("%w: validate ACP session settings: %w", ports.ErrChatResumeFailed, err)
		}
	}
	launchCfg := LaunchConfig{
		Policy:    cfg.Policy,
		SessionID: cfg.SessionID, DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath,
		Env:   cfg.Env,
		Model: cfg.Model, Permissions: cfg.Permissions, SystemPrompt: cfg.SystemPrompt,
		ProviderScopeID: cfg.ProviderScopeID,
		ReconnectOnly:   cfg.ReconnectOnly,
		// The connect below spawns a fresh process, so a resumed conversation's
		// provider-side history has to be reachable from the new process home.
		ProviderConversationID: cfg.ProviderConversationID,
	}
	conv, init, live, err := d.connect(ctx, launchCfg, cfg.PrepareEnv)
	if err != nil {
		return nil, err
	}
	if live != nil {
		if live.SessionID != cfg.ProviderConversationID {
			conv.discard()
			return nil, fmt.Errorf("%w: persistent ACP session %q does not match %q",
				ports.ErrChatRecoveryInconclusive, live.SessionID, cfg.ProviderConversationID)
		}
		var setup struct {
			ConfigOptions []acpsdk.SessionConfigOption `json:"configOptions,omitempty"`
			Modes         *acpsdk.SessionModeState     `json:"modes,omitempty"`
			Models        *legacySessionModelState     `json:"models,omitempty"`
		}
		if len(live.SessionResult) == 0 {
			conv.discard()
			return nil, fmt.Errorf("%w: persistent ACP session has no setup response", ports.ErrChatRecoveryInconclusive)
		}
		if decodeErr := json.Unmarshal(live.SessionResult, &setup); decodeErr != nil {
			conv.discard()
			return nil, fmt.Errorf("%w: decode persistent ACP session: %w", ports.ErrChatRecoveryInconclusive, decodeErr)
		}
		if d.cfg.ValidateTurnSettings != nil {
			if err := d.cfg.ValidateTurnSettings(ports.PermissionMode(live.InitialPermissions), ports.ChatTurnSettings{
				Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions,
			}); err != nil {
				conv.discard()
				return nil, fmt.Errorf("%w: live ACP launch settings: %w", ports.ErrChatRecoveryInconclusive, err)
			}
		}
		conv.liveState = live
		conv.hasDurableTurns = cfg.HasPriorTurns
		conv.start(
			cfg.ProviderConversationID, conversationCapabilities(d.cfg.Capabilities, init),
			d.sessionModeFor(launchCfg.Policy), d.cfg.SessionOptions, d.permissionPolicyFor(launchCfg.Policy),
			cfg.Permissions, d.cfg.ValidateTurnSettings, setup.ConfigOptions,
			setup.Models, setup.Modes,
		)
		conv.setReportedProviderConversationID(reportedProviderConversationID)
		conv.initialPermission = ports.PermissionMode(live.InitialPermissions)
		conv.setSessionPreamble(d.sessionPreamble(launchCfg))
		return conv, nil
	}
	return d.resumeSession(ctx, conv, init, launchCfg, cfg, reportedProviderConversationID)
}

func (d *Driver) resumeSession(
	ctx context.Context,
	conv *conversation,
	init acpsdk.InitializeResponse,
	launchCfg LaunchConfig,
	cfg ports.ChatResumeConfig,
	reportedProviderConversationID string,
) (ports.ChatConversation, error) {
	if !supportsSessionRestore(init) {
		conv.discard()
		return nil, fmt.Errorf("%w: ACP agent supports neither session/load nor session/resume", ports.ErrChatResumeFailed)
	}
	additional, err := normalizeAdditionalDirectories(cfg.WorkspacePath, cfg.AdditionalDirectories,
		init.AgentCapabilities.SessionCapabilities.AdditionalDirectories != nil)
	if err != nil {
		conv.discard()
		if isACPAuthRequired(err) {
			return nil, normalizeACPError("ACP session/resume", err)
		}
		return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
	}
	mcpServers, err := normalizeMCPServers(cfg.MCPServers, init.AgentCapabilities.McpCapabilities)
	if err != nil {
		conv.discard()
		return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
	}

	resumeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	meta := map[string]any(nil)
	if d.cfg.SessionMeta != nil {
		meta = d.cfg.SessionMeta(launchCfg)
	}
	var configOptions []acpsdk.SessionConfigOption
	var modes *acpsdk.SessionModeState
	var historyConversation *refreshableConversation
	if init.AgentCapabilities.LoadSession {
		historyConversation = newRefreshableConversation(conv, acpsdk.LoadSessionRequest{
			Meta:                  meta,
			SessionId:             acpsdk.SessionId(cfg.ProviderConversationID),
			Cwd:                   cfg.WorkspacePath,
			AdditionalDirectories: additional,
			McpServers:            mcpServers,
		})
		historyConversation.closeBeforeReload = d.cfg.CloseSessionBeforeReload &&
			init.AgentCapabilities.SessionCapabilities.Close != nil
		loadStarted := time.Now()
		resp, err := historyConversation.loadHistory(resumeCtx)
		d.logStartStage(launchCfg.SessionID, "session_load", loadStarted, err)
		if err != nil {
			if cfg.FreshIfMissing && !cfg.ReconnectOnly && isACPResourceNotFound(err) {
				return d.resumeFresh(ctx, conv, init, launchCfg, cfg, additional, mcpServers)
			}
			conv.discard()
			return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, missingConversation(normalizeACPLoadError("ACP session/load", err), err))
		}
		configOptions = resp.ConfigOptions
		modes = resp.Modes
	} else {
		resumeStarted := time.Now()
		resp, err := conv.conn.ResumeSession(resumeCtx, acpsdk.ResumeSessionRequest{
			Meta:                  meta,
			SessionId:             acpsdk.SessionId(cfg.ProviderConversationID),
			Cwd:                   cfg.WorkspacePath,
			AdditionalDirectories: additional,
			McpServers:            mcpServers,
		})
		d.logStartStage(launchCfg.SessionID, "session_resume", resumeStarted, err)
		if err != nil {
			if cfg.FreshIfMissing && !cfg.ReconnectOnly && isACPResourceNotFound(err) {
				return d.resumeFresh(ctx, conv, init, launchCfg, cfg, additional, mcpServers)
			}
			conv.discard()
			return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, missingConversation(err, err))
		}
		configOptions = resp.ConfigOptions
		modes = resp.Modes
	}
	conv.hasDurableTurns = cfg.HasPriorTurns
	conv.start(
		cfg.ProviderConversationID, conversationCapabilities(d.cfg.Capabilities, init),
		d.sessionModeFor(launchCfg.Policy), d.cfg.SessionOptions, d.permissionPolicyFor(launchCfg.Policy),
		cfg.Permissions, d.cfg.ValidateTurnSettings, configOptions,
		conv.legacyWire.modelState(), modes,
	)
	conv.setReportedProviderConversationID(reportedProviderConversationID)
	// Standing context is process input, not transcript history, so a resumed
	// conversation must receive it again even though the provider recovers the
	// messages itself.
	conv.setSessionPreamble(d.sessionPreamble(launchCfg))
	if err := d.applyLaunchSessionOptions(ctx, conv, launchCfg); err != nil {
		conv.discard()
		return nil, fmt.Errorf("%w: configure ACP session: %w", ports.ErrChatResumeFailed, err)
	}
	if err := conv.applyTurnSettings(ctx, ports.ChatTurnSettings{Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions}); err != nil {
		if !errors.Is(err, ErrACPSetterUnsupported) {
			conv.discard()
			return nil, fmt.Errorf("%w: configure ACP session: %w", ports.ErrChatResumeFailed, err)
		}
	}
	if historyConversation != nil {
		return historyConversation, nil
	}
	return conv, nil
}

// resumeFresh opens a new provider session in place of a stored one the
// provider reports does not exist. Only a newly spawned provider reaches it: a
// surviving host that still holds the session is reattached before any reload.
// The caller's FreshIfMissing asserts the stored conversation never started, so
// no history is lost; the returned conversation reports the new id.
func (d *Driver) resumeFresh(
	ctx context.Context,
	conv *conversation,
	init acpsdk.InitializeResponse,
	launchCfg LaunchConfig,
	cfg ports.ChatResumeConfig,
	additional []string,
	mcpServers []acpsdk.McpServer,
) (ports.ChatConversation, error) {
	settings := ports.ChatTurnSettings{Model: cfg.Model, Effort: cfg.Effort, Approval: cfg.Permissions}
	if err := d.openNewSession(ctx, conv, init, launchCfg, settings, additional, mcpServers); err != nil {
		conv.discard()
		return nil, fmt.Errorf("%w: start fresh in place of missing ACP session: %w", ports.ErrChatResumeFailed, err)
	}
	d.log.Info("chat: ACP session missing; started fresh",
		"sessionID", cfg.SessionID, "harness", d.cfg.Harness)
	return conv, nil
}
