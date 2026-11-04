package claudecode

import (
	"context"
	"log/slog"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/hooksjson"
	"tallpine.eu/tallpine/backend/internal/ports"
)

const (
	claudeSettingsDirName   = ".claude"
	claudeSettingsFileName  = "settings.local.json"
	claudeHookCommandPrefix = "tallpine hooks claude-code "
	// claudeHookTimeout (seconds) is Claude's kill switch for a hook that never
	// returns. `tallpine hooks` gives up on the daemon well before it (see the CLI's
	// hookReportBudget), so this only bounds a wedged process.
	claudeHookTimeout = 10
)

// claudeSessionStartMatcher is referenced by pointer so SessionStart serializes
// with Claude's documented source matcher. "startup" alone misses --resume
// relaunches (and /clear, compact, fork), so the native session id is not
// confirmed under the new RuntimeLaunchID until the next user prompt (#4122).
var claudeSessionStartMatcher = "startup|resume|clear|compact|fork"

// claudeManagedHooks is the source of truth for the hooks Tallpine installs:
// SessionStart (startup/resume/clear/compact/fork), UserPromptSubmit, the tool-use
// trio (PreToolUse, PostToolUse, PostToolUseFailure), PermissionRequest,
// Stop, StopFailure, Notification, and SessionEnd. They report normalized
// session metadata and activity-state signals back into Tallpine's store (see
// DeriveActivityState).
// Notification and SessionEnd carry no matcher: each installs once and fires
// for every sub-type, and the handler filters on the payload's
// notification_type / reason field. The tool-use hooks also carry no matcher
// (fire for every tool): their payloads carry tool_name/tool_use_id, which
// lifecycle uses to clear a stale sticky `blocked` only when the specific
// approved tool finishes — the daemon-side precedence rule is what makes these
// signals safe against parallel-subagent traffic (the naive mapping without it
// was reverted in PR #5's review). PermissionRequest fires when a permission
// dialog appears and carries the blocking tool_name; for worker sessions
// `tallpine hooks` writes nothing to stdout, so installing it never injects a
// permission decision. Headless reviewer sessions (TALLPINE_REVIEW_SESSION_ID set)
// are the one exception: nobody can answer the dialog there, so the hook
// replies with an allow/deny decision (see cli.reviewerPermissionDecision, #4810).
var claudeManagedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Matcher: &claudeSessionStartMatcher, Command: claudeHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: claudeHookCommandPrefix + "user-prompt-submit"},
	{Event: "PreToolUse", Command: claudeHookCommandPrefix + "pre-tool-use"},
	{Event: "PostToolUse", Command: claudeHookCommandPrefix + "post-tool-use"},
	{Event: "PostToolUseFailure", Command: claudeHookCommandPrefix + "post-tool-use-failure"},
	{Event: "PermissionRequest", Command: claudeHookCommandPrefix + "permission-request"},
	{Event: "Stop", Command: claudeHookCommandPrefix + "stop"},
	// StopFailure fires instead of Stop when an API error (rate limit, usage
	// limit, unknown model, expired login) ends the turn. Without it such a
	// turn left the session active.
	{Event: "StopFailure", Command: claudeHookCommandPrefix + "stop-failure"},
	{Event: "Notification", Command: claudeHookCommandPrefix + "notification"},
	{Event: "SubagentStart", Command: claudeHookCommandPrefix + "subagent-start"},
	{Event: "SubagentStop", Command: claudeHookCommandPrefix + "subagent-stop"},
	{Event: "SessionEnd", Command: claudeHookCommandPrefix + "session-end"},
}

// claudeHooks removes Tallpine's hooks from the workspace-local
// .claude/settings.local.json file, where earlier versions installed them. They
// now ride the --settings launch flag (see claudeHooksSettings); a leftover
// copy there would run every hook twice.
var claudeHooks = hooksjson.Manager{
	Label:                 "claude-code",
	CommandPrefix:         claudeHookCommandPrefix,
	LegacyCommandPrefixes: []string{"tallpine hooks continue "},
	Timeout:               claudeHookTimeout,
	Path:                  claudeSettingsPath,
	Managed:               claudeManagedHooks,
}

func claudeSettingsPath(workspacePath string) string {
	return filepath.Join(workspacePath, claudeSettingsDirName, claudeSettingsFileName)
}

// GetAgentHooks no longer writes hooks into the workspace: Tallpine's Claude Code hooks
// are delivered in the --settings launch flag, out of the agent's reach. It only
// strips a copy left in settings.local.json by an earlier version, preserving
// user-defined hooks and unrelated settings, so restore and resume do not run
// each hook twice.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := claudeHooks.Uninstall(ctx, cfg.WorkspacePath); err != nil {
		// The cleanup is best effort: Tallpine no longer depends on this file, and a
		// malformed one (the agent can write it) must not block spawn or restore.
		slog.Warn("claude-code: could not strip legacy hooks from settings.local.json", "workspace", cfg.WorkspacePath, "error", err)
	}
	return nil
}

// UninstallHooks removes Tallpine's Claude Code hooks, leaving user-defined hooks untouched.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	return claudeHooks.Uninstall(ctx, workspacePath)
}

// AreHooksInstalled reports whether any Tallpine Claude Code hook is present.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	return claudeHooks.AreInstalled(ctx, workspacePath)
}
