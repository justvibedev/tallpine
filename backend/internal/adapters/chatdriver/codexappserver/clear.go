package codexappserver

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Same reason as the other optional capabilities: losing the method does not
// break a build, it silently turns /clear back into a refused command.
var _ ports.ChatContextClearer = (*conversation)(nil)

func cloneParams(params map[string]any) map[string]any {
	return maps.Clone(params)
}

// threadSettings are the launch settings every thread/start, thread/resume and
// thread/fork request carries. threadStartParams is the one place that turns them
// into request parameters, so a setting added here reaches launch, resume, fork
// and /clear alike.
type threadSettings struct {
	WorkspacePath string
	Policy        agentpolicy.Decision
	Permissions   ports.PermissionMode
	ReadOnly      bool
	Model         string
	Effort        string
	MCPServers    []ports.ChatMCPServerConfig
	Env           map[string]string
	SystemPrompt  string
}

func threadStartParams(s threadSettings) map[string]any {
	policy, sandbox, reviewer := launchApprovalSettings(s.Policy, s.Permissions, s.ReadOnly)
	params := map[string]any{
		"cwd":               s.WorkspacePath,
		"approvalPolicy":    policy,
		"approvalsReviewer": reviewer,
		"sandbox":           sandbox,
	}
	if s.Model != "" {
		params["model"] = s.Model
	}
	// There is no top-level effort field; carry the durable Tallpine choice as a
	// config override so a thread does not fall back to the provider default.
	config := threadConfig(s.Effort, s.MCPServers, s.Env)
	if sandbox == "workspace-write" {
		home, _ := os.UserHomeDir()
		network, roots := permissioncap.CodexWorkspaceWriteExtras(home, s.Policy.GitRepo(), s.WorkspacePath)
		ws := map[string]any{"network_access": network}
		if len(roots) > 0 {
			ws["writable_roots"] = roots
		}
		config["sandbox_workspace_write"] = ws
	}
	if len(config) > 0 {
		params["config"] = config
	}
	if s.SystemPrompt != "" {
		params["developerInstructions"] = s.SystemPrompt
	}
	return params
}

// resumeFreshThread rebuilds the thread/start request for a live-reconnected
// host, which skipped the launch handshake that normally records it.
func resumeFreshThread(cfg ports.ChatResumeConfig) map[string]any {
	return threadStartParams(threadSettings{
		WorkspacePath: cfg.WorkspacePath, Policy: cfg.Policy, Permissions: cfg.Permissions, ReadOnly: cfg.ReadOnly,
		Model: cfg.Model, Effort: cfg.Effort, MCPServers: cfg.MCPServers, Env: cfg.Env, SystemPrompt: cfg.SystemPrompt,
	})
}

// ClearContext moves the conversation onto a brand-new thread opened with the
// same launch settings (model, effort, auto-compact window, approvals, sandbox,
// MCP servers, standing instructions). app-server v2 has no rollback or clear
// RPC, but thread/start on the same connection yields an empty thread.
//
// commit persists the new id before the conversation switches to it; if commit
// fails the conversation stays on the old thread, so memory and the durable
// binding can never disagree. The caller must hold the conversation idle.
func (c *conversation) ClearContext(ctx context.Context, commit func(newProviderConversationID string) error) error {
	if len(c.freshThread) == 0 {
		return errors.New("codex: no launch settings recorded for a replacement thread")
	}
	var resp struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		Model           string `json:"model"`
		ReasoningEffort string `json:"reasoningEffort"`
	}
	if err := c.conn.request(ctx, "thread/start", cloneParams(c.freshThread), &resp); err != nil {
		return fmt.Errorf("thread/start: %w", err)
	}
	if resp.Thread.ID == "" {
		return errors.New("thread/start returned no thread id")
	}
	if err := commit(resp.Thread.ID); err != nil {
		return err
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.threadMu.Lock()
	c.threadID = resp.Thread.ID
	c.threadMu.Unlock()
	c.threadModel, c.threadEffort = resp.Model, resp.ReasoningEffort
	// A new thread carries none of the old one's sticky state.
	c.fastTier, c.planMode = false, false
	c.tierUnknown, c.planUnknown = false, false
	return nil
}
