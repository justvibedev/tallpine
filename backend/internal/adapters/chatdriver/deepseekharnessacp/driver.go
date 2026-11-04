// Package deepseekharnessacp binds the user's own DeepSeek Harness installation to
// Tallpine's reusable ACP Chat transport.
package deepseekharnessacp

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/deepseekharness"
	acpdriver "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/acp"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Plugin is the slice of the DeepSeek Harness agent plugin the driver reuses for
// installation discovery, authentication and runtime-env policy.
type Plugin interface {
	ResolveBinary(context.Context) (string, error)
	AuthStatus(context.Context) (ports.AgentAuthStatus, error)
	AugmentRuntimeEnv(env map[string]string, dataDir string)
}

// New launches `dsh --profile acp` from the exact binary resolved by the
// DeepSeek Harness agent plugin; Tallpine never downloads or substitutes it. The
// profile supplies the model route and credentials; Tallpine adds only the privacy
// overlay and DSH_TELEMETRY_MODE=DISABLED (the plugin's AugmentRuntimeEnv, the
// same policy the terminal path gets).
func New(plugin Plugin, log *slog.Logger) ports.ChatDriver {
	return acpdriver.New(buildConfig(plugin, log), log)
}

func buildConfig(plugin Plugin, log *slog.Logger) acpdriver.Config {
	usage := newUsageTracker(log)
	return acpdriver.Config{
		Harness: domain.HarnessDeepSeek,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true,
			ports.ChatCapabilityTools:     true,
			ports.ChatCapabilityApprovals: true,
			ports.ChatCapabilityInterrupt: true,
			ports.ChatCapabilityResume:    true,
			ports.ChatCapabilityUsage:     true,
		},
		Probe: func(ctx context.Context) error {
			if _, err := plugin.ResolveBinary(ctx); err != nil {
				return fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			status, err := plugin.AuthStatus(ctx)
			if err == nil && status == ports.AgentAuthStatusUnauthorized {
				return ports.ErrChatAuthRequired
			}
			if err != nil && log != nil {
				// An inconclusive local probe is not evidence the agent cannot
				// run; the ACP handshake remains authoritative.
				log.Debug("deepseek-harness auth probe inconclusive; continuing", "error", err)
			}
			return nil
		},
		Launch: func(ctx context.Context, cfg acpdriver.LaunchConfig) (acpdriver.Launch, error) {
			binary, err := plugin.ResolveBinary(ctx)
			if err != nil {
				return acpdriver.Launch{}, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			launch, err := launchFor(plugin, binary, cfg.Env, cfg.DataDir, cfg.Permissions, cfg.Policy)
			if err == nil {
				launch, err = fenceLaunch(launch, cfg.Policy, cfg.Permissions, cfg.WorkspacePath)
			}
			if err == nil {
				usage.noteLaunch(launch.Env)
			}
			return launch, err
		},
		FencesLaunch:         deepseekharness.FencesLaunch,
		SessionOptions:       sessionOptions,
		SessionPreamble:      SessionPreamble,
		PermissionPolicy:     permissionPolicy,
		ValidateTurnSettings: validateTurnSettings,
		SessionUsage:         usage.sessionUsage,
	}
}

// launchFor is the one place a dsh ACP process is described: the profile plus
// privacy overlay, over a copy of env with the plugin's runtime policy applied.
func launchFor(plugin Plugin, binary string, env map[string]string, dataDir string, mode ports.PermissionMode, decision agentpolicy.Decision) (acpdriver.Launch, error) {
	args, err := deepseekharness.ACPArgs(dataDir)
	if err != nil {
		return acpdriver.Launch{}, err
	}
	out := maps.Clone(env)
	if out == nil {
		out = map[string]string{}
	}
	plugin.AugmentRuntimeEnv(out, dataDir)
	limit := permissioncap.Of(decision)
	if limit.ForbidsBypass() {
		stripFullAccessMode(out)
	}
	if ports.NormalizePermissionMode(limit.Lower(domain.HarnessDeepSeek, mode)) == ports.PermissionModeBypassPermissions && out[permissionModeEnv] == "" {
		// The acp profile reads DSH_PERMISSION_MODE for both the sandbox mode and
		// the approval policy (`dsh --profile acp --dump-config`: sandbox-policy
		// and approval). danger-full-access means no sandbox and approval "never",
		// so a bypass session never asks and git can reach a worktree's common dir.
		out[permissionModeEnv] = "danger-full-access"
	}
	return acpdriver.Launch{Command: binary, Args: args, Env: out}, nil
}

// DiscoveryLaunch is the launch model discovery uses to read the model catalog a
// short-lived ACP session advertises. It carries the same overlay and telemetry
// env as a real session, so listing models never leaves the privacy defaults.
func DiscoveryLaunch(binary string, env map[string]string, dataDir string) (acpdriver.Launch, error) {
	return launchFor(deepseekharness.New(), binary, env, dataDir, ports.PermissionModeDefault, agentpolicy.Decision{})
}

// permissionModeEnv is the dsh variable the acp profile reads for its sandbox
// mode and approval policy.
const permissionModeEnv = "DSH_PERMISSION_MODE"

// sandboxedModes are the DSH_PERMISSION_MODE values dsh's acp profile documents
// as keeping a sandbox (the profile compares them exactly, so no trimming or
// case folding). dsh's own vocabulary is not Tallpine's, so under a cap only these are
// trusted; anything else, empty or unknown, is dropped and dsh falls back to its
// own workspace-write default.
var sandboxedModes = map[string]bool{"read-only": true, "workspace-write": true}

// stripFullAccessMode removes a DSH_PERMISSION_MODE that is not known to be
// sandboxed, whatever its source (project or daemon environment). Left in place
// under a policy cap that forbids bypass, danger-full-access or any alias would
// launch dsh with no sandbox and approval "never", which no answer from
// permissionPolicy can undo. The key is matched case-insensitively because
// Windows environments are.
func stripFullAccessMode(env map[string]string) {
	for key, value := range env {
		if strings.EqualFold(key, permissionModeEnv) && !sandboxedModes[value] {
			delete(env, key)
		}
	}
}

// deepseekGuidance is static so the prompt prefix stays cacheable. dsh's edit
// tool refuses files it has not read through its own read tool.
const deepseekGuidance = "Read a file with the read tool (use offset/limit for ranges) before editing it; " +
	"shell reads such as sed, cat or grep don't satisfy the edit tool. " +
	"If `git add` or `git commit` is denied by the sandbox, run `tallpine commit -m \"<message>\"` instead: " +
	"it commits all changes in this session's worktree for you (it never pushes)."

// deepseekEfforts maps Tallpine's provider-neutral reasoning-effort vocabulary onto
// the levels DeepSeek Harness advertises (off, low, high, max). Harness has no
// middle level, so Tallpine's "medium" takes Harness's own default balance. Values not
// listed here pass through unchanged: a model that grows a level keeps working,
// and an unrecognized value is rejected by the agent rather than silently
// replaced.
// "medium" maps to "high" on purpose: dsh has no middle level, and high is its
// own default balance.
var deepseekEfforts = map[string]string{
	"none":    "off",
	"minimal": "off",
	"off":     "off",
	"low":     "low",
	"medium":  "high",
	"high":    "high",
	"xhigh":   "max",
	"max":     "max",
}

// sessionOptions maps Tallpine's per-turn choices onto DeepSeek Harness's ACP config
// options. Model values are opaque: Harness encodes each choice as a JSON array
// string of the form ["deepseek-official","deepseek-v4-flash"], and the value Tallpine
// received from the session catalog is the value it sends back.
func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	options := make([]acpdriver.SessionOption, 0, 2)
	if model := strings.TrimSpace(settings.Model); model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: model})
	}
	if effort := strings.TrimSpace(settings.Effort); effort != "" {
		options = append(options, acpdriver.SessionOption{ID: "reasoning_effort", Value: deepseekEffort(effort)})
	}
	if len(options) == 0 {
		return nil
	}
	return options
}

// SessionPreamble delivers Tallpine's standing instructions as a text block ahead of
// the first prompt. DeepSeek Harness accepts no system-prompt launch flag and
// its session/new handler reads only the working directory, so plain text on
// the first turn is the channel it follows — the same shape as agyacp. An
// embedded resource is avoided: Antigravity-class agents treated the resource
// URI as something to look up rather than as instructions in testing.
func SessionPreamble(cfg acpdriver.LaunchConfig) []acpsdk.ContentBlock {
	standing := strings.TrimSpace(cfg.SystemPrompt)
	if !cfg.DisableTools {
		if standing != "" {
			standing += "\n\n"
		}
		standing += deepseekGuidance
	}
	if standing == "" {
		return nil
	}
	return []acpsdk.ContentBlock{acpsdk.TextBlock(
		"Standing instructions from Tallpine. Follow them for this whole session:\n" +
			"<ao_standing_instructions>\n" + standing + "\n</ao_standing_instructions>",
	)}
}

// deepseekEffort resolves one Tallpine effort value to a Harness level, passing an
// already-native or unknown value through untouched.
func deepseekEffort(effort string) string {
	if mapped, ok := deepseekEfforts[strings.ToLower(effort)]; ok {
		return mapped
	}
	return effort
}

// ValidateTurnSettings rejects a turn setting that cannot reach the session, so
// the failure surfaces before DAO creates durable state rather than part-way
// through the first turn. Harness model values are the opaque strings its own
// session catalog advertises, so the model is only checked for presence; the
// effort must resolve to one of the four advertised levels.
func validateTurnSettings(initial ports.PermissionMode, settings ports.ChatTurnSettings) error {
	// Bypass is fixed at launch (DSH_PERMISSION_MODE, see launchFor): a process
	// started unsandboxed cannot be re-sandboxed by a mode switch or by recovery
	// reusing it, so leaving bypass is refused. Entering bypass needs no relaunch;
	// permissionPolicy auto-allows.
	if ports.NormalizePermissionMode(initial) == ports.PermissionModeBypassPermissions &&
		settings.Approval != "" && ports.NormalizePermissionMode(settings.Approval) != ports.PermissionModeBypassPermissions {
		return fmt.Errorf("%w: this DeepSeek Harness chat started in bypass-permissions with its sandbox off; restart the chat to change sandbox mode", ports.ErrChatConfigOptionInvalid)
	}
	return ValidateEffort(settings.Effort)
}

// ValidateEffort checks a Tallpine effort value against the levels Harness
// advertises. Empty is valid (no override).
func ValidateEffort(value string) error {
	effort := strings.TrimSpace(value)
	if effort == "" {
		return nil
	}
	switch deepseekEffort(effort) {
	case "off", "low", "high", "max":
		return nil
	default:
		return fmt.Errorf("%w: DeepSeek Harness reasoning effort %q is not one of off, low, high, max", ports.ErrChatConfigOptionInvalid, value)
	}
}

// permissionPolicy is Tallpine's side of accept-edits and auto. DeepSeek Harness asks
// through session/request_permission with one-shot allow/reject choices and
// expects the client to answer, so Tallpine answers per request rather than granting
// anything up front. Bypass launches Harness with approval "never" (see
// launchFor); answering allow here is the safety net should it ask anyway.
func permissionPolicy(
	decision agentpolicy.Decision,
	mode ports.PermissionMode,
	params acpsdk.RequestPermissionRequest,
) (acpsdk.PermissionOptionId, bool) {
	// Capped here, not only at launch: the turn mode can change per turn or be
	// restored from the stored conversation, and this is what auto-allows.
	mode = ports.NormalizePermissionMode(permissioncap.Of(decision).LowerLive(domain.HarnessDeepSeek, mode))
	kind := params.ToolCall.Kind
	if mode != ports.PermissionModeAuto && mode != ports.PermissionModeBypassPermissions && (mode != ports.PermissionModeAcceptEdits || kind == nil ||
		(*kind != acpsdk.ToolKindEdit && *kind != acpsdk.ToolKindDelete && *kind != acpsdk.ToolKindMove)) {
		return "", false
	}
	for _, option := range params.Options {
		if option.Kind == acpsdk.PermissionOptionKindAllowOnce {
			return option.OptionId, true
		}
	}
	return "", false
}
