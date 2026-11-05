// Package claudecode implements the Claude Code agent adapter.
//
// It builds the argv to launch `claude` as an interactive session inside a
// session's worktree, installs worktree-local hooks that report normalized
// session metadata (native id, title, summary) back into Tallpine's store,
// and supports resume: GetLaunchCommand pins either Tallpine's requested native UUID
// or a stable Tallpine-session-derived fallback so GetRestoreCommand can rebuild
// `claude --resume <uuid>`. SessionInfo reads the
// hook-captured metadata from the store — it does not parse transcripts.
// GetConfigSpec remains a no-op (no agent-specific config keys yet).
//
// Claude Code starts an interactive session by default (no -p/--print), which
// is exactly what Tallpine wants: a live agent the user can attach to in the
// browser terminal or via `tmux attach`. The initial task prompt is passed
// as the positional argument; the orchestrator system prompt (if any) is
// appended to Claude's default system prompt so its built-in coding
// instructions are preserved.
package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/nativeconfig"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/adapters"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/agentbase"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/binaryutil"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
	"tallpine.eu/tallpine/backend/pkg/agentruntime"
)

const (
	// adapterID is the registry id and the value users pass to
	// `tallpine spawn --agent`.
	adapterID = "claude-code"
)

// Plugin is the Claude Code agent adapter. It is safe for concurrent use; the
// binary path is resolved once and cached under binaryMu.
type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New returns a ready-to-register Claude Code adapter.
func New() *Plugin {
	return &Plugin{}
}

// EmitsSubmitActivity signals that Claude Code fires a user-prompt-submit hook
// under Tallpine's launch, so Activity.State can flip to active after a prompt is
// accepted. See ports.SubmitActivitySignaler.
func (p *Plugin) EmitsSubmitActivity() bool { return true }

// EmitsSemanticMessageAcceptance reports that UserPromptSubmit includes the
// accepted prompt, allowing Tallpine delivery ids to be correlated semantically.
func (p *Plugin) EmitsSemanticMessageAcceptance() bool { return true }

// EmitsBlockedActivity signals that Claude Code fires both pre- and post-tool
// hooks, so Activity.State can flip to blocked mid-turn on a permission dialog
// and the guarded send loop can clear it once the tool completes. Only
// claude-code (and its hook-delegators) carry this trio; see
// ports.BlockedActivitySignaler.
func (p *Plugin) EmitsBlockedActivity() bool { return true }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.AgentAuthDiagnosticsChecker = (*Plugin)(nil)
var _ ports.EmptyComposerDetector = (*Plugin)(nil)
var _ ports.AgentInterfaceHandoff = (*Plugin)(nil)
var _ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
var _ ports.TerminalSurfaceInspector = (*Plugin)(nil)

// ComposerIsEmpty recognizes Claude Code's blank composer or its dim
// placeholder. Claude renders normal, non-dim status chrome below a bordered
// composer, so inspect that bounded region before using the footer-free fallback.
// A permission-menu selection and normal typed text are rejected.
func (p *Plugin) ComposerIsEmpty(output string) bool {
	return p.InspectTerminalSurface(output).Composer == ports.TerminalComposerEmpty
}

// Manifest returns the adapter's static self-description.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Claude Code",
		Description: "Run Claude Code worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

// permissionConfigEnum lists the permission modes the "permissions" config key
// accepts. It mirrors the ports.PermissionMode constants so a project's stored
// config validates against the same vocabulary the launch command maps.
var permissionConfigEnum = []string{
	string(ports.PermissionModeDefault),
	string(ports.PermissionModeAcceptEdits),
	string(ports.PermissionModeAuto),
	string(ports.PermissionModeBypassPermissions),
}

// GetConfigSpec reports the per-project agent config keys Claude Code
// understands: a model override and a starting permission mode.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ConfigSpec{}, err
	}
	return ports.ConfigSpec{
		Fields: []ports.ConfigField{
			{
				Key:         "model",
				Type:        ports.ConfigFieldString,
				Description: "Model override passed to `claude --model` (e.g. claude-opus-4-5).",
			},
			{
				Key:  "effort",
				Type: ports.ConfigFieldString,
				Description: "Reasoning level passed to `claude --effort`. Valid levels are " +
					"per-model and come from the provider's own catalog, so this is not a fixed enum.",
			},
			{
				Key:         "permissions",
				Type:        ports.ConfigFieldEnum,
				Description: "Starting permission mode.",
				Enum:        permissionConfigEnum,
			},
		},
	}, nil
}

// GetLaunchCommand builds the argv to start an interactive Claude Code
// session. Shape:
//
//	claude [--session-id <uuid>] \
//	       [--permission-mode <mode> --settings <defaultMode: mode>] \
//	       [--append-system-prompt-file <path> | --append-system-prompt <text>] \
//	       [-- <prompt>]
//
// --session-id pins Claude's native session UUID to LaunchConfig.NativeSessionID
// when Tallpine requests a distinct provider conversation, otherwise to a value
// derived from the Tallpine session id for the legacy one-conversation path. This
// makes the session resumable later (see
// GetRestoreCommand) and its transcript is locatable (see SessionInfo) without
// a separate capture step.
//
// <mode> is acceptEdits, auto, or bypassPermissions. Tallpine's "default"
// mode emits no --permission-mode flag, so Claude's TUI resolves the starting
// mode from ~/.claude/settings.json exactly as a normal launch. An explicit
// mode is repeated as the flag-settings defaultMode only to keep Claude's
// auto-mode default dialog out of the session (see autoDefaultNudgeGuardArgs).
//
// The prompt is passed after `--` so a prompt beginning with "-" is not
// mistaken for a flag.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	// Defense-in-depth: the project service validates on write, but re-check
	// here so a config written by any other path can't launch a bad command.
	if err := cfg.Config.Validate(); err != nil {
		return nil, fmt.Errorf("claude-code: %w", err)
	}

	binary, err := p.claudeBinary(ctx)
	if err != nil {
		return nil, err
	}

	// A project's configured permissions drive the starting mode; the explicit
	// LaunchConfig.Permissions wins when set so a per-spawn override still takes
	// precedence over the stored project default.
	permissions := cfg.Permissions
	if permissions == "" {
		permissions = cfg.Config.Permissions
	}
	nativeSessionID, err := p.freshLaunchNativeSessionID(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return agentruntime.BuildLaunchCommand(agentruntime.LaunchConfig{
		Harness:          agentruntime.HarnessClaudeCode,
		Binary:           binary,
		SessionID:        cfg.SessionID,
		NativeSessionID:  nativeSessionID,
		ProviderArgs:     claudeProviderArgs(cfg.Config.Launch, agentruntime.PermissionPolicy(permissions), cfg.Env, cfg.Policy, cfg.SkipRepoAgentConfig),
		Model:            cfg.Config.Model,
		Effort:           cfg.Config.Effort,
		Prompt:           cfg.Prompt,
		SystemPrompt:     cfg.SystemPrompt,
		SystemPromptFile: cfg.SystemPromptFile,
		Permission:       agentruntime.PermissionPolicy(permissions),
		AllowedTools:     cfg.AllowedTools,
		DisallowedTools:  cfg.DisallowedTools,
	})
}

// claudeLaunchSettingsArgs passes a session's launch choices to Claude Code as
// settings: the session's own launch options (addLaunchOptionSettings) and
// what the agent policy decides (the protectState sandbox). They share one
// --settings flag with the auto-mode dialog guard (see autoDefaultNudgeGuardArgs).
func claudeLaunchSettingsArgs(options domain.AgentLaunchOptions, policy agentruntime.PermissionPolicy, env map[string]string, decision agentpolicy.Decision) []string {
	settings := launchModeSettings(policy)
	denyDaemonTokenRead(settings, env)
	if protectStateApplies(decision, runtime.GOOS) {
		mergeProtectState(settings, env, decision)
	}
	settings["hooks"] = claudeHooksSettings()
	// Pinned in the flag layer so a disableAllHooks in the worktree's local or
	// project settings cannot switch Tallpine's hooks off.
	settings["disableAllHooks"] = false
	addLaunchOptionSettings(settings, options)
	encoded, _ := json.Marshal(settings)
	return []string{"--settings", string(encoded)}
}

// denyDaemonTokenRead adds Claude Code permission deny rules for Tallpine's
// secrets and store: the install token (daemontoken.Path of the launch's run
// file), the session key that scoped tokens are MACed under, the database, the
// GitHub PAT, the browser runtime key and the Electron profile (cookies). The
// agent holds a scoped session token and talks to the daemon over its socket, so
// it never needs any of these, and all sit readable by the same OS user. This is
// a guard for Claude's own tools, not a sandbox: a shell command can still cat
// the files. No run file in the launch env means no rule.
func denyDaemonTokenRead(settings map[string]any, env map[string]string) {
	runFile := strings.TrimSpace(env[brand.Env("RUN_FILE")])
	if runFile == "" {
		return
	}
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
		settings["permissions"] = perms
	}
	perms["deny"] = protectedStateReadRules(runFile, env[brand.Env("DATA_DIR")])
}

// claudeReadRule is a Read permission rule for an absolute path. Claude Code
// treats "/x" as relative to the settings root; an absolute filesystem path
// needs "//x", and a Windows drive path is written "//c/Users/...".
func claudeReadRule(path string) string {
	p := filepath.ToSlash(path)
	if len(p) >= 2 && p[1] == ':' {
		p = "/" + strings.ToLower(p[:1]) + p[2:]
	}
	return "Read(/" + p + ")"
}

// claudeProviderArgs is every Claude flag Tallpine derives from the session's launch
// options, permission policy and repo-config choice. Launch and restore share it.
func claudeProviderArgs(options domain.AgentLaunchOptions, mode agentruntime.PermissionPolicy, env map[string]string, decision agentpolicy.Decision, skipRepoConfig bool) []string {
	args := claudeLaunchSettingsArgs(options, mode, env, decision)
	args = append(args, claudePolicyModeArgs(decision, mode)...)
	return append(args, claudeRepoConfigArgs(skipRepoConfig, decision)...)
}

// freshLaunchNativeSessionID keeps a fresh launch from adopting someone else's
// conversation. The legacy id is derived from the Tallpine session id alone, while
// Claude's transcripts live in one global config dir, so a reused Tallpine id (a
// wiped or second data dir) maps onto an old transcript that --session-id would
// then continue. A fresh launch has no prior conversation of its own, so when
// the derived id already has a transcript a new id is used instead; hooks
// capture whichever id Claude ends up with.
func (p *Plugin) freshLaunchNativeSessionID(ctx context.Context, cfg ports.LaunchConfig) (string, error) {
	if cfg.NativeSessionID != "" || cfg.SessionID == "" {
		return cfg.NativeSessionID, nil
	}
	taken, err := p.NativeConversationExists(ctx, ports.SessionRef{}, claudeSessionUUID(cfg.SessionID), nil)
	if err != nil {
		return "", err
	}
	if taken {
		return p.NewNativeSessionID(), nil
	}
	return "", nil
}

// PreLaunch is an optional capability the spawn engine invokes (via type
// assertion) immediately before creating the session. Claude Code shows a
// blocking "do you trust this folder?" dialog the first time it runs in any
// directory. Every Tallpine worktree is a fresh path, so without this the
// agent would hang at that prompt with no one to answer it.
//
// A Tallpine worktree is derived from the repo the user is already running
// Tallpine in, so it is inherently trusted. PreLaunch records that trust in the
// global config the launched Claude reads (~/.claude.json by default, see
// claudeLaunchConfigPaths) before launch, additively and atomically, so it
// cannot clobber a concurrently-running Claude instance's config. For a launch
// in Tallpine's default permission mode it also marks the auto-mode default dialog
// seen there, only when the user's settings would make Claude offer it (see
// autoDefaultNudgeGuardArgs); explicit modes are covered per session instead.
func (p *Plugin) PreLaunch(ctx context.Context, cfg ports.LaunchConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.WorkspacePath == "" {
		return nil
	}
	settingsPath, cfgPath, err := claudeLaunchConfigPaths(cfg.Env)
	if err != nil {
		return err
	}
	permissions := cfg.Permissions
	if permissions == "" {
		permissions = cfg.Config.Permissions
	}
	markNudgeSeen := len(autoDefaultNudgeGuardArgs(agentruntime.PermissionPolicy(permissions))) == 0 &&
		userSettingsOfferAutoDefaultNudge(settingsPath)
	return ensureClaudeLaunchConfigForOS(cfgPath, cfg.WorkspacePath, markNudgeSeen, runtime.GOOS)
}

// claudeLaunchConfigPaths returns the user settings file and the global config
// file a Claude launched with env reads, both resolved from the same
// directory. A CLAUDE_CONFIG_DIR the launch environment sets (the session's
// project env) wins over the daemon's own, as in NativeSessionConfigDir. With
// one set, settings live in that directory and the global config is its
// .claude.json; without, they are ~/.claude/settings.json and ~/.claude.json.
// Claude prefers a non-empty legacy .config.json in the config directory over
// either global config path, so that file is used when present.
func claudeLaunchConfigPaths(env map[string]string) (settingsPath, globalConfigPath string, err error) {
	configDir, globalConfigPath, err := nativeconfig.ClaudeConfigDir(env)
	if err != nil {
		return "", "", fmt.Errorf("claude-code: resolve config directory: %w", err)
	}
	return filepath.Join(configDir, "settings.json"), globalConfigPath, nil
}

// Claude Code (verified in 2.1.287 through 2.1.302; the gate that enables it
// defaults on from about 2.1.285) shows a one-time "Make auto mode your default
// permission mode?" dialog once the session is idle, when the global config has
// completed onboarding without hasSeenAutoDefaultNudge, user settings set a
// permissions.defaultMode other than "auto", and no project, local, flag, or
// policy settings set a defaultMode. The dialog takes the keyboard: text Tallpine
// pastes into the pane is swallowed and its Enter picks the focused "Yes",
// which switches the running session to auto mode and rewrites the user's
// settings.json defaultMode to "auto".
//
// autoDefaultNudgeGuardArgs suppresses it per session for an explicit Tallpine
// permission mode by also passing that mode as the flag-settings defaultMode.
// The --permission-mode flag still decides the starting mode (it outranks every
// settings defaultMode), so the session's mode and the user's files are
// unchanged. Tallpine's default mode passes no flag and returns nil here: mirroring
// the user's defaultMode would change the mode whenever the project environment
// points Claude at another CLAUDE_CONFIG_DIR, so PreLaunch marks the dialog
// seen in the global config instead.
func autoDefaultNudgeGuardArgs(policy agentruntime.PermissionPolicy) []string {
	settings := autoDefaultNudgeGuardSettings(policy)
	if settings == nil {
		return nil
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return nil
	}
	return []string{"--settings", string(encoded)}
}

// autoDefaultNudgeGuardSettings is the flag-settings content of
// autoDefaultNudgeGuardArgs, or nil when the mode needs no guard.
func autoDefaultNudgeGuardSettings(policy agentruntime.PermissionPolicy) map[string]any {
	flag := agentruntime.ClaudePermissionArgs(policy)
	if len(flag) != 2 {
		return nil
	}
	return map[string]any{"permissions": map[string]any{"defaultMode": flag[1]}}
}

// userSettingsOfferAutoDefaultNudge reports whether the user's own Claude
// settings carry the defaultMode that makes Claude offer the auto-mode default
// dialog. Unreadable or unparsable settings report false: Tallpine then leaves the
// global config alone.
func userSettingsOfferAutoDefaultNudge(settingsPath string) bool {
	data, err := os.ReadFile(settingsPath) //nolint:gosec // the user's own Claude settings path
	if err != nil {
		return false
	}
	var settings struct {
		Permissions struct {
			DefaultMode string `json:"defaultMode"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return false
	}
	mode := strings.TrimSpace(settings.Permissions.DefaultMode)
	return mode != "" && mode != "auto"
}

// GetRestoreCommand rebuilds the argv that continues an existing Claude Code
// session: `claude [--model <model>] [--permission-mode <mode>]
// --resume <agentSessionId>`. It prefers the hook-captured native session id
// from cfg.Session.Metadata["agentSessionId"]; for sessions created before
// hooks captured it, it falls back to the deterministic UUID Tallpine pins via
// --session-id at launch. ok is false only when neither is available, so the
// caller fresh-spawns. The command applies the caller's model selection,
// permission mode, and current standing system instructions. A blank model
// leaves model selection to Claude. When Prompt is present it is passed as the
// resume-time user turn, avoiding a fragile terminal paste into Claude's TUI.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if _, ok := agentruntime.RestoreIdentity(
		agentruntime.HarnessClaudeCode,
		cfg.Session.ID,
		cfg.Session.Metadata,
	); !ok {
		return nil, false, nil
	}

	binary, err := p.claudeBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	return agentruntime.BuildRestoreCommand(agentruntime.RestoreConfig{
		Harness:   agentruntime.HarnessClaudeCode,
		Binary:    binary,
		SessionID: cfg.Session.ID,
		Metadata:  cfg.Session.Metadata,
		ProviderArgs: claudeProviderArgs(cfg.Config.Launch, agentruntime.PermissionPolicy(cfg.Permissions), statefence.RestoreEnv(cfg.RunFilePath, cfg.DataDir, cfg.Session),
			cfg.Policy, cfg.SkipRepoAgentConfig),
		Model:            cfg.Config.Model,
		Effort:           cfg.Config.Effort,
		Prompt:           cfg.Prompt,
		SystemPrompt:     cfg.SystemPrompt,
		SystemPromptFile: cfg.SystemPromptFile,
		Permission:       agentruntime.PermissionPolicy(cfg.Permissions),
		AllowedTools:     cfg.AllowedTools,
		DisallowedTools:  cfg.DisallowedTools,
	})
}

// SessionInfo surfaces the normalized session metadata that the Claude Code
// hooks persisted into Tallpine's store: the native session id, the title (the
// first user prompt), and the summary (the final assistant message). It reads
// only from session.Metadata — never from transcript files — and returns
// ok=false when none of those fields are present. Metadata is intentionally nil:
// there is no Claude-specific field callers need beyond the normalized ones.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

// NativeConversationID bridges Claude Code's terminal and ACP surfaces. Both
// use the same native Claude session UUID. Tallpine terminal sessions pin a
// deterministic UUID, while Chat persists the id returned by claude-agent-acp.
func (p *Plugin) NativeConversationID(
	ctx context.Context,
	session ports.SessionRef,
	currentMode domain.SessionMode,
	providerConversationID string,
) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if currentMode == domain.SessionModeChat {
		id := strings.TrimSpace(providerConversationID)
		return id, id != "", nil
	}
	id := strings.TrimSpace(session.Metadata[ports.MetadataKeyAgentSessionID])
	if id == "" && session.ID != "" {
		id = claudeSessionUUID(session.ID)
	}
	return id, id != "", nil
}

// NativeConversationExists distinguishes Claude's reserved session UUID from
// a conversation that Claude has actually persisted. Claude Code accepts
// --session-id before the first prompt, but does not create its JSONL transcript
// until the conversation has content. Passing that reserved-but-empty UUID to
// either `claude --resume` or ACP session/load returns "No conversation found".
//
// The Agent SDK documents local transcripts at
// ~/.claude/projects/<project-key>/<session-id>.jsonl (or beneath
// CLAUDE_CONFIG_DIR). We only test for a non-empty top-level transcript; Tallpine does
// not parse or project provider files here.
func (p *Plugin) NativeConversationExists(
	ctx context.Context,
	_ ports.SessionRef,
	nativeConversationID string,
	env map[string]string,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	id := strings.TrimSpace(nativeConversationID)
	if !isUUID(id) {
		return false, nil
	}
	// Resolve the root exactly as the launched Claude will: an env entry (even
	// an explicitly empty one) overrides the daemon's CLAUDE_CONFIG_DIR.
	configDir, err := p.NativeSessionConfigDir(ctx, env)
	if err != nil {
		return false, fmt.Errorf("claude-code: resolve transcript root: %w", err)
	}
	_, ok, err := findTranscript(ctx, configDir, id)
	return ok, err
}

func isUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

// claudeSessionUUID maps a Tallpine session id onto a stable Claude Code
// session UUID via UUIDv5 over a fixed namespace, so the same Tallpine session
// always resolves to the same Claude session.
func claudeSessionUUID(tallpineSessionID string) string {
	return agentruntime.ClaudeSessionID(tallpineSessionID)
}

// SessionUUID maps a Tallpine session id onto the native Claude Code session UUID
// used by --session-id and --resume.
func SessionUUID(tallpineSessionID string) string {
	return claudeSessionUUID(tallpineSessionID)
}

// claudeBinarySpec locates the claude binary: PATH first, then the native
// installer's locations, npm global, Homebrew, and the claude-managed dir.
var claudeBinarySpec = binaryutil.BinarySpec{
	Label:         "claude",
	Names:         []string{"claude"},
	WinNames:      []string{"claude.exe", "claude.cmd", "claude"},
	UnixPaths:     []string{"/usr/local/bin/claude", "/opt/homebrew/bin/claude"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("claude", []string{".claude", "local", "claude"}),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "claude.exe"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "claude.cmd"}},
	},
}

// ResolveClaudeBinary returns the path to the claude binary, or a wrapped
// ports.ErrAgentBinaryNotFound when it is absent.
func ResolveClaudeBinary(ctx context.Context) (string, error) {
	binary, err := binaryutil.ResolveBinary(ctx, claudeBinarySpec)
	if err != nil {
		return "", err
	}
	return resolveNativeWindowsClaude(binary, runtime.GOOS), nil
}

// resolveNativeWindowsClaude unwraps the npm-generated command shim to the
// native executable shipped by Claude Code. The ACP bridge passes this path to
// Node child_process.spawn, which cannot execute .cmd files directly on current
// Node releases.
func resolveNativeWindowsClaude(path, goos string) string {
	if goos != "windows" || !strings.EqualFold(filepath.Ext(path), ".cmd") {
		return path
	}
	for _, candidate := range windowsNativeClaudeCandidatesForShim(path) {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return path
}

func windowsNativeClaudeCandidatesForShim(shim string) []string {
	dir := filepath.Dir(shim)
	candidates := []string{
		filepath.Join(dir, "claude.exe"),
		filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe"),
	}
	// Tallpine's managed launcher lives apart from the npm prefix it starts; the
	// native executable is in the current managed release.
	if current, ok := binaryutil.ManagedCurrentDir(shim, "claude-code"); ok {
		candidates = append(candidates, filepath.Join(current, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe"))
	}
	return candidates
}

func (p *Plugin) claudeBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveClaudeBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

// InvalidateBinaryResolution forgets the cached executable path, so the next
// launch resolves it again: after Tallpine installs a managed copy, for example.
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}

// SupportsTerminalVersionRefresh opts into exact-conversation idle restarts.
// The restore command re-applies session options and explicitly selects the
// captured native conversation rather than selecting the latest session.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return true }

var _ ports.AgentTerminalVersionRefresher = (*Plugin)(nil)
