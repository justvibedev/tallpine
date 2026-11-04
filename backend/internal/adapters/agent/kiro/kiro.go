// Package kiro implements the Kiro (AWS) agent adapter: launching interactive
// sessions, resuming hook-tracked sessions, installing workspace-local hooks,
// and reading hook-derived session info.
//
// Kiro is AWS's agentic coding assistant. Its terminal CLI ships as the
// `kiro-cli` binary. Tallpine launches Kiro with a workspace-local custom agent so
// both worker and orchestrator sessions can use Kiro's normal interactive
// approval flow. See https://kiro.dev/docs/cli/headless/ and
// https://kiro.dev/docs/cli/reference/cli-commands/.
//
// Worker prompts are delivered after startup so Tallpine keeps Kiro's interactive
// TUI. Permission/approval modes are written as `permissions`
// rules in the generated agent profile (see profile.go); only bypass adds
// `--trust-all-tools`.
// Restore uses `kiro-cli chat --resume-id <UUID>` with the native session id
// captured from a Kiro hook payload.
//
// Tallpine-managed sessions derive native session identity and display metadata from
// Kiro's native hooks (see hooks.go) rather than transcript scans.
package kiro

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/agentbase"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/binaryutil"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

// Plugin is the Kiro agent adapter. It is safe for concurrent use; the binary
// path is resolved once and cached under binaryMu.
type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
	// sessionsRoot overrides ~/.kiro/sessions (tests).
	sessionsRoot string
}

// New returns a ready-to-register Kiro adapter.
func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)

// Manifest returns the adapter's static self-description.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          "kiro",
		Name:        "Kiro",
		Description: "Run Kiro (AWS) worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

// GetConfigSpec reports the per-project agent config keys Kiro understands:
// a model override. Kiro already forwards agentConfig.Model into its
// workspace-local custom agent config (see setKiroAgentDefaults in
// hooks.go); this declares that support so it is discoverable/validated
// through the config-spec surface, matching claude-code and codex.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelEffortConfigSpec(ctx,
		"Model override passed to `kiro-cli chat --model` and written into Kiro's workspace-local agent config.",
		"Reasoning effort passed to `kiro-cli chat --effort`.")
}

// GetLaunchCommand builds the argv to start a new Kiro session:
// `kiro-cli chat [--agent tallpine] --agent tallpine [trust flags] [-- <prompt>]`.
//
// The prompt is passed as a positional argument after `--` so a leading "-" is
// not read as a flag for non-worker launches. Worker prompts are sent after
// startup so Tallpine keeps the interactive TUI and avoids Kiro's current positional
// input submission gap. Kiro runs interactively for both workers and
// orchestrators; standing instructions come from the generated custom agent.
// Tallpine standing instructions are installed during workspace preparation through
// the Tallpine-managed workspace-local agent config, then selected here with --agent.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.kiroBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary, "chat", "--v3", "--trust-workspace", "--agent", kiroAgentName}
	appendModelEffortFlags(&cmd, cfg.Config)
	appendApprovalFlags(&cmd, cfg.Permissions)

	prompt := cfg.Prompt
	if prompt != "" && cfg.Kind == domain.KindOrchestrator {
		cmd = append(cmd, "--", prompt)
	}

	return Fence(cmd, cfg.Env, cfg.Policy, cfg.WorkspacePath)
}

// GetPromptDeliveryStrategy reports how Kiro receives the initial task prompt.
// Orchestrator standing instructions are delivered through the generated
// custom-agent prompt, so no command or post-start prompt injection is needed
// there.
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if cfg.Prompt != "" && cfg.Kind == domain.KindOrchestrator {
		return ports.PromptDeliveryInCommand, nil
	}
	if cfg.Prompt != "" && cfg.Kind != domain.KindOrchestrator {
		return ports.PromptDeliveryAfterStart, nil
	}
	if cfg.Kind == domain.KindOrchestrator {
		return ports.PromptDeliveryCustomAgent, nil
	}
	return ports.PromptDeliveryInCommand, nil
}

// PromptReadinessHints waits for Kiro's interactive prompt before Tallpine injects
// the worker's first task.
func (p *Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	if err := ctx.Err(); err != nil {
		return ports.PromptReadinessHints{}, err
	}
	return ports.PromptReadinessHints{
		InitialDelay: 500 * time.Millisecond,
		Patterns:     []string{"ask a question or describe a task"},
		PollInterval: 200 * time.Millisecond,
		Timeout:      8 * time.Second,
		Lines:        80,
	}, nil
}

// GetRestoreCommand rebuilds the argv that continues an existing Kiro session:
// `kiro-cli chat --no-interactive --resume-id <agentSessionId> [trust flags]`.
// ok is false when the hook-derived native session id has not landed yet, so
// callers can fall back to fresh launch behavior.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.kiroBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = make([]string, 0, 8)
	// v3 reads sessions created by v2, so restore is always v3.
	cmd = append(cmd, binary, "chat", "--v3", "--trust-workspace", "--agent", kiroAgentName, "--resume-id", agentSessionID)
	appendModelEffortFlags(&cmd, cfg.Config)
	appendApprovalFlags(&cmd, cfg.Permissions)
	cmd, err = Fence(cmd, statefence.RestoreEnv(cfg.RunFilePath, cfg.DataDir, cfg.Session), cfg.Policy, cfg.Session.WorkspacePath)
	return cmd, err == nil, err
}

// SessionInfo surfaces Kiro hook-derived metadata. Metadata is intentionally
// nil for Kiro: callers get the normalized fields directly.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

var kiroBinarySpec = binaryutil.BinarySpec{
	Label:         "kiro",
	Names:         []string{"kiro-cli"},
	WinNames:      []string{"kiro-cli.cmd", "kiro-cli.exe", "kiro-cli"},
	UnixPaths:     []string{"/usr/local/bin/kiro-cli", "/opt/homebrew/bin/kiro-cli"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("kiro-cli", []string{".kiro", "bin", "kiro-cli"}),
	NodeManaged:   true,
	// The native Kiro installer location is probed before the npm shim, matching
	// the pre-refactor order so a native install still wins when both are present.
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinLocalAppData, Parts: []string{"Programs", "kiro", "kiro-cli.exe"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "kiro-cli.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "kiro-cli.exe"}},
		{Base: binaryutil.WinHome, Parts: []string{".kiro", "bin", "kiro-cli.exe"}},
	},
}

// ResolveKiroBinary returns the path to the kiro-cli binary on this machine,
// searching PATH then a handful of well-known install locations. It returns a
// wrapped ports.ErrAgentBinaryNotFound when kiro-cli is absent.
func ResolveKiroBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, kiroBinarySpec)
}

// AgentProfileName is the workspace-local custom agent Tallpine writes for every
// Kiro session, terminal or chat.
const AgentProfileName = kiroAgentName

// HasAgentProfile reports whether workspacePath holds the Tallpine custom agent.
// Session workspaces always do; the scratch directory of a background task
// (title and branch naming) does not, so it must not ask Kiro for the profile.
func HasAgentProfile(workspacePath string) bool {
	info, err := os.Stat(kiroAgentPath(workspacePath))
	return err == nil && info.Mode().IsRegular()
}

func (p *Plugin) kiroBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveKiroBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

// appendModelEffortFlags passes the model and effort on argv.
func appendModelEffortFlags(cmd *[]string, cfg ports.AgentConfig) {
	if model := strings.TrimSpace(cfg.Model); model != "" {
		*cmd = append(*cmd, "--model", model)
	}
	agentbase.AppendEffortFlag(cmd, cfg, "--effort")
}

// appendApprovalFlags maps Tallpine's permission modes onto Kiro's tool-trust flags.
// Default emits no flag so Kiro uses its normal interactive approval flow.
// accept-edits and auto ride on the agent-profile permissions rules (see
// kiroPermissionRules); only bypass grants all tools.
func appendApprovalFlags(cmd *[]string, permissions ports.PermissionMode) {
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeDefault:
		// No flag: defer to Kiro's normal interactive approval flow.
	case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto:
		// No flag: the agent profile's permissions rules (workspace read/write /
		// shell allow-list) carries these modes; see kiroPermissionRules.
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--trust-all-tools")
	}
}

// InvalidateBinaryResolution forgets the cached executable path, so the next
// launch resolves it again: after Tallpine installs a managed copy, for example.
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}
