// Package kiroacp binds the user's own Kiro CLI (v3 engine) to Tallpine's reusable
// ACP Chat transport.
package kiroacp

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/kiro"
	acpdriver "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/acp"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Plugin is the slice of the Kiro agent plugin the driver reuses for
// installation discovery and authentication.
type Plugin interface {
	ResolveBinary(context.Context) (string, error)
	AuthStatus(context.Context) (ports.AgentAuthStatus, error)
}

// New launches `kiro-cli acp --agent-engine v3` from the executable the Kiro agent
// plugin resolved; Tallpine never downloads or substitutes it.
//
// Kiro's v3 ACP server rejects --agent, --model, --effort and --trust-all-tools,
// so everything those flags would do goes through ACP instead: the workspace's
// `tallpine` custom agent (the profile chat spawn already writes, carrying the
// system prompt, permission rules and hooks the terminal path uses) is selected
// as the session mode, and the model through the model config option.
func New(plugin Plugin, log *slog.Logger) ports.ChatDriver {
	return acpdriver.New(buildConfig(plugin, log), log)
}

func buildConfig(plugin Plugin, log *slog.Logger) acpdriver.Config {
	return acpdriver.Config{
		Harness: domain.HarnessKiro,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming:     true,
			ports.ChatCapabilityTools:         true,
			ports.ChatCapabilityApprovals:     true,
			ports.ChatCapabilityInterrupt:     true,
			ports.ChatCapabilityResume:        true,
			ports.ChatCapabilityModels:        true,
			ports.ChatCapabilityConfigOptions: true,
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
				log.Debug("kiro auth probe inconclusive; continuing", "error", err)
			}
			return nil
		},
		Launch: func(ctx context.Context, cfg acpdriver.LaunchConfig) (acpdriver.Launch, error) {
			binary, err := plugin.ResolveBinary(ctx)
			if err != nil {
				return acpdriver.Launch{}, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			return launchFor(binary, cfg)
		},
		FencesState: kiro.FencesState,
		// Fail visibly when the profile or model is not advertised rather than
		// letting Kiro acknowledge a mode switch it did not apply.
		RequireAdvertisedOptions: true,
		// Kiro publishes its model catalog shortly after session/new answers.
		OptionCatalogWait:    3 * time.Second,
		LaunchSessionOptions: launchSessionOptions,
		SessionOptions:       sessionOptions,
		PermissionPolicy:     permissionPolicy,
		ValidateTurnSettings: validateTurnSettings,
		TurnUsage:            turnUsage,
		InfoContextUsage:     infoContextUsage,
	}
}

// launchFor describes the Kiro ACP process. --auth-method cli reuses the user's
// kiro-cli login, the same credential the terminal path uses. Under Security
// mode on macOS the process runs inside the same seatbelt fence as the terminal.
func launchFor(binary string, cfg acpdriver.LaunchConfig) (acpdriver.Launch, error) {
	out := maps.Clone(cfg.Env)
	if out == nil {
		out = map[string]string{}
	}
	argv, err := kiro.Fence([]string{binary, "acp", "--agent-engine", "v3", "--auth-method", "cli"}, out, cfg.Policy, cfg.WorkspacePath)
	if err != nil {
		return acpdriver.Launch{}, err
	}
	return acpdriver.Launch{Command: argv[0], Args: argv[1:], Env: out}, nil
}

// launchSessionOptions selects the Tallpine custom agent. Kiro lists custom agents
// as values of its `mode` option; the agent is fixed for the session. A
// background task (title and branch naming) runs in a scratch directory where
// no profile is written, so it keeps Kiro's own agent rather than requesting a
// mode Kiro does not offer.
func launchSessionOptions(cfg acpdriver.LaunchConfig) []acpdriver.SessionOption {
	if !kiro.HasAgentProfile(cfg.WorkspacePath) {
		return nil
	}
	return []acpdriver.SessionOption{{ID: "mode", Value: kiro.AgentProfileName}}
}

// sessionOptions maps the per-turn model onto Kiro's `model` option. Kiro v3
// advertises no reasoning-effort option, so effort is not sent.
func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	if model := strings.TrimSpace(settings.Model); model != "" {
		return []acpdriver.SessionOption{{ID: "model", Value: model}}
	}
	return nil
}

// permissionPolicy is Tallpine's side of accept-edits, auto and bypass. Kiro asks
// through session/request_permission, tagging the request with the capability it
// guards (_meta.kiro.consent.capability). The profile's permission rules already
// allow what each mode grants, so this is the safety net: accept-edits and auto
// answer file writes, bypass answers everything. Auto does not blanket-allow
// shell, because the profile's reviewed command allow-list is its boundary;
// anything else still goes to a human.
func permissionPolicy(
	decision agentpolicy.Decision,
	mode ports.PermissionMode,
	params acpsdk.RequestPermissionRequest,
) (acpsdk.PermissionOptionId, bool) {
	mode = ports.NormalizePermissionMode(permissioncap.Of(decision).LowerLive(domain.HarnessKiro, mode))
	switch mode {
	case ports.PermissionModeBypassPermissions:
	case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto:
		if consentCapability(params.Meta) != "fs_write" {
			return "", false
		}
	default:
		return "", false
	}
	for _, option := range params.Options {
		if option.Kind == acpsdk.PermissionOptionKindAllowOnce {
			return option.OptionId, true
		}
	}
	return "", false
}

// consentCapability reads _meta.kiro.consent.capability from a permission request.
func consentCapability(meta any) string {
	root, _ := meta.(map[string]any)
	kiroMeta, _ := root["kiro"].(map[string]any)
	consent, _ := kiroMeta["consent"].(map[string]any)
	capability, _ := consent["capability"].(string)
	return capability
}

// validateTurnSettings refuses to lower the permission mode below the launch
// mode. The profile's allow rules are written once at spawn and Kiro applies
// them itself without asking, so a lower mode would be recorded by Tallpine while
// Kiro kept acting under the broader rules. Raising needs no relaunch: the
// permission policy grants the extra.
func validateTurnSettings(initial ports.PermissionMode, settings ports.ChatTurnSettings) error {
	if settings.Approval == "" {
		return nil
	}
	launched := ports.NormalizePermissionMode(initial)
	requested := ports.NormalizePermissionMode(settings.Approval)
	if requested.Rank() < launched.Rank() {
		return fmt.Errorf("%w: this Kiro chat started in %s and its profile already allows that; restart the chat to lower permissions", ports.ErrChatConfigOptionInvalid, launched)
	}
	return nil
}

// turnUsage reads the credits one turn cost from a Kiro prompt response:
// _meta.kiro.promptTurnSummaries. Kiro reports no tokens, so the row carries
// credits and one turn, like the terminal transcript reader. The model is the
// session's current model; an unnamed one is recorded as "kiro".
func turnUsage(meta map[string]any, model string) []domain.ModelUsage {
	kiroMeta, _ := meta["kiro"].(map[string]any)
	rows, _ := kiroMeta["promptTurnSummaries"].([]any)
	summaries := make([]kiro.PromptTurnSummary, 0, len(rows))
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		unit, _ := row["unit"].(string)
		usage, _ := row["usage"].(float64)
		summaries = append(summaries, kiro.PromptTurnSummary{Unit: unit, Usage: usage})
	}
	credits, ok := kiro.CreditsFromSummaries(summaries)
	if !ok {
		return nil
	}
	if model == "" {
		model = "kiro"
	}
	return []domain.ModelUsage{{Model: model, Credits: credits, Turns: 1}}
}

// contextScale is the window infoContextUsage reports: Kiro gives a percentage,
// not token counts, and the UI reads used/window.
const contextScale = 100

// infoContextUsage reads the context percentage kiro-cli 3.x puts in
// session_info_update _meta.kiro.contextUsage.usagePercentage (it sends no
// usage_update).
func infoContextUsage(meta map[string]any) (used, window int64, ok bool) {
	kiroMeta, _ := meta["kiro"].(map[string]any)
	usage, _ := kiroMeta["contextUsage"].(map[string]any)
	pct, isNum := usage["usagePercentage"].(float64)
	if !isNum || pct < 0 {
		return 0, 0, false
	}
	return int64(math.Round(math.Min(pct, contextScale))), contextScale, true
}
