package codexappserver

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strconv"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/codex"
	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/contextwindow"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// Start opens a new Codex thread in the session worktree.
func (d *Driver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if !cfg.ProviderIDsScoped {
		cfg.ProviderScopeID = ""
	}
	conv, params, err := d.openFreshHost(ctx, cfg, "persistent chat host already owns a provider conversation for a fresh session")
	if err != nil {
		return nil, err
	}
	if cfg.Ephemeral {
		params["ephemeral"] = true
	}

	var resp threadOpened
	openCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	if err := conv.conn.request(openCtx, "thread/start", params, &resp); err != nil {
		_ = conv.Terminate()
		return nil, fmt.Errorf("thread/start: %w", err)
	}
	if resp.Thread.ID == "" {
		_ = conv.Terminate()
		return nil, errors.New("thread/start returned no thread id")
	}

	conv.start(resp.Thread.ID, resp.Model, resp.ReasoningEffort)
	return conv, nil
}

// threadConfig is the config override sent with thread/start and
// thread/resume. Codex layers it above config.toml for this thread only.
//
// Tallpine's own "tallpine" server (html_preview, html_render) is pre-approved. Tallpine answers
// no mcpServer/elicitation/request, so a prompt could only fail the call.
// Verified live on codex-cli 0.160.1: a tool not marked read-only raised that
// request under on-request and was refused under never/read-only, and
// default_tools_approval_mode "approve" removed both.
func threadConfig(effort string, servers []ports.ChatMCPServerConfig, env map[string]string) map[string]any {
	config := map[string]any{}
	if env[agentlaunch.EnvGitGuardRoot] != "" {
		maps.Copy(config, codex.GitGuardThreadConfig())
	}
	if tokens, err := strconv.Atoi(env[contextwindow.CodexAutoCompactLimitEnv]); err == nil && tokens > 0 {
		config["model_auto_compact_token_limit"] = tokens
	}
	if effort != "" {
		config["model_reasoning_effort"] = effort
	}
	if len(servers) == 0 {
		return config
	}
	mcpServers := make(map[string]any, len(servers))
	for _, server := range servers {
		entry := map[string]any{}
		if server.Name == brand.MCPServerName {
			entry["default_tools_approval_mode"] = "approve"
		}
		if server.Type == "http" {
			entry["url"] = server.URL
			if len(server.Headers) > 0 {
				entry["http_headers"] = server.Headers
			}
		} else {
			entry["command"] = server.Command
			if len(server.Args) > 0 {
				entry["args"] = server.Args
			}
			if len(server.Env) > 0 {
				entry["env"] = server.Env
			}
		}
		mcpServers[server.Name] = entry
	}
	config["mcp_servers"] = mcpServers
	return config
}

// ForkIntoHost starts an inclusive native fork in an independent provider host.
func (d *Driver) ForkIntoHost(ctx context.Context, sourceProviderConversationID, lastProviderTurnID string, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if sourceProviderConversationID == "" || lastProviderTurnID == "" {
		return nil, errors.New("source thread and exact turn anchor are required")
	}
	conv, params, err := d.openFreshHost(ctx, cfg, "side provider host already owns a conversation")
	if err != nil {
		return nil, err
	}
	params["threadId"] = sourceProviderConversationID
	params["lastTurnId"] = conv.nativeID(lastProviderTurnID)
	// The child retains native context; copied turns are not needed in this response.
	params["excludeTurns"] = true
	openCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	var resp threadOpened
	if err := conv.conn.request(openCtx, "thread/fork", params, &resp); err != nil {
		_ = conv.Terminate()
		return nil, fmt.Errorf("thread/fork in side host: %w", err)
	}
	if resp.Thread.ID == "" || resp.Thread.ID == sourceProviderConversationID {
		_ = conv.Terminate()
		return nil, errors.New("thread/fork returned no independent thread id")
	}
	if cfg.RecordFork != nil {
		if err := cfg.RecordFork(context.WithoutCancel(ctx), resp.Thread.ID); err != nil {
			_ = conv.Terminate()
			return nil, fmt.Errorf("record Codex fork: %w", err)
		}
	}
	conv.start(resp.Thread.ID, resp.Model, resp.ReasoningEffort)
	return conv, nil
}

// threadOpened is the part of a thread/start or thread/fork response a new
// conversation needs.
type threadOpened struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort"`
}

// requireAbsoluteWorkspace refuses a relative workspace path: app-server
// resolves a relative cwd against its own process directory, which would
// silently put the agent in the wrong tree.
func requireAbsoluteWorkspace(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("workspace path must be absolute, got %q", path)
	}
	return nil
}

// openFreshHost connects the session's provider host for a conversation that
// does not exist yet (a new thread or a fork) and returns it with the launch
// settings applied and the thread/start params built from cfg. A host that
// already owns a conversation is a durable-state mismatch, not permission to
// destroy a provider that may still be working, so it is closed untouched and
// reported as alreadyOwned.
func (d *Driver) openFreshHost(ctx context.Context, cfg ports.ChatStartConfig, alreadyOwned string) (*conversation, map[string]any, error) {
	if err := requireAbsoluteWorkspace(cfg.WorkspacePath); err != nil {
		return nil, nil, err
	}
	conv, reconnected, err := d.connectSession(
		ctx, cfg.SessionID, cfg.DataDir, cfg.WorkspacePath, cfg.Env, cfg.PrepareEnv, cfg.ProviderScopeID, false,
		appServerFence(cfg.Policy, cfg.Permissions, cfg.ReadOnly, cfg.WorkspacePath),
	)
	if err != nil {
		return nil, nil, err
	}
	if reconnected {
		_ = conv.Close()
		return nil, nil, errors.New(alreadyOwned)
	}
	conv.readOnly = cfg.ReadOnly
	conv.disableNetwork = cfg.DisableNetwork
	conv.launchMode = cfg.Permissions
	conv.policy = cfg.Policy
	conv.workspace = cfg.WorkspacePath
	params := threadStartParams(threadSettings{
		WorkspacePath: cfg.WorkspacePath, Policy: cfg.Policy, Permissions: cfg.Permissions, ReadOnly: cfg.ReadOnly,
		Model: cfg.Model, Effort: cfg.Effort, MCPServers: cfg.MCPServers, Env: cfg.Env, SystemPrompt: cfg.SystemPrompt,
	})
	return conv, params, nil
}
