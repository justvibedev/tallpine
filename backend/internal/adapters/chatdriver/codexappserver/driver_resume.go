package codexappserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// Reconnect attaches to a surviving provider without launching a replacement.
func (d *Driver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	cfg.ReconnectOnly = true
	return d.Resume(ctx, cfg)
}

// Resume reattaches to a stored Codex thread after a daemon or app-server
// restart. A thread that is still running is rejoined rather than restarted.
func (d *Driver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if !cfg.ProviderIDsScoped {
		cfg.ProviderScopeID = ""
	}
	if cfg.ProviderConversationID == "" {
		return nil, fmt.Errorf("%w: no stored thread id", ports.ErrChatResumeFailed)
	}
	if err := requireAbsoluteWorkspace(cfg.WorkspacePath); err != nil {
		return nil, err
	}

	conv, reconnected, err := d.connectSession(
		ctx, cfg.SessionID, cfg.DataDir, cfg.WorkspacePath, cfg.Env, cfg.PrepareEnv, cfg.ProviderScopeID, cfg.ReconnectOnly,
		appServerFence(cfg.Policy, cfg.Permissions, cfg.ReadOnly, cfg.WorkspacePath),
	)
	if err != nil {
		return nil, err
	}
	conv.readOnly = cfg.ReadOnly
	conv.launchMode = cfg.Permissions
	conv.policy = cfg.Policy
	conv.workspace = cfg.WorkspacePath
	if reconnected {
		// The host preserved the already-initialized app-server connection and its
		// loaded thread. Host replay bridges output and unresolved server requests
		// across the daemon detach without waiting for the active turn to settle.
		// Neither the tier nor the collaboration mode the thread was left on is
		// known; the next turn states both.
		conv.tierUnknown, conv.planUnknown = true, true
		conv.start(cfg.ProviderConversationID, cfg.Model, cfg.Effort)
		return conv, nil
	}

	params := threadStartParams(threadSettings{
		WorkspacePath: cfg.WorkspacePath, Policy: cfg.Policy, Permissions: cfg.Permissions, ReadOnly: cfg.ReadOnly,
		Model: cfg.Model, Effort: cfg.Effort, MCPServers: cfg.MCPServers, Env: cfg.Env, SystemPrompt: cfg.SystemPrompt,
	})
	params["threadId"] = cfg.ProviderConversationID
	resumeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	var resp struct {
		Model           string  `json:"model"`
		ReasoningEffort string  `json:"reasoningEffort"`
		ServiceTier     *string `json:"serviceTier"`
	}
	err = conv.conn.request(resumeCtx, "thread/resume", params, &resp)
	if err != nil && cfg.FreshIfMissing && codexThreadMissing(err) {
		// The caller proved the thread never had a turn (a switch target that
		// had not answered), so Codex never saved it: a new thread loses nothing.
		delete(params, "threadId")
		var started threadOpened
		if err = conv.conn.request(resumeCtx, "thread/start", params, &started); err == nil && started.Thread.ID == "" {
			err = errors.New("thread/start returned no thread id")
		}
		if err != nil {
			_ = conv.Terminate()
			return nil, fmt.Errorf("%w: start in place of a missing thread: %w", ports.ErrChatResumeFailed, err)
		}
		conv.start(started.Thread.ID, started.Model, started.ReasoningEffort)
		return conv, nil
	}
	if err != nil {
		_ = conv.Terminate()
		// Otherwise not falling back to thread/start: silently opening a new
		// conversation would present unrelated history as continuous.
		return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
	}

	// A tier sticks to the thread. One Tallpine left it on is undone by the first turn
	// here; one the user configured is theirs, as on a new thread.
	conv.fastTier = resp.ServiceTier != nil && conv.configuredServiceTier(ctx) == ""
	// thread/resume does not report the collaboration mode, and Plan sticks to
	// the thread across a restart, so the next turn states it.
	conv.planUnknown = true
	conv.start(cfg.ProviderConversationID, resp.Model, resp.ReasoningEffort)
	return conv, nil
}

// codexThreadMissing reports a thread/resume refused because Codex has no
// saved thread under that id: none at all, or the empty rollout a thread that
// never had a turn leaves behind ("rollout at … is empty").
func codexThreadMissing(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no rollout found") ||
		strings.Contains(message, "thread not found") ||
		(strings.Contains(message, "rollout") && strings.Contains(message, "is empty"))
}
