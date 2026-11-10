package sessionmanager

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/modelcatalog"
	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// effectiveHarness resolves the harness for a spawn: an explicit harness wins;
// otherwise the project's role override for the session kind applies. Empty is
// invalid for new worker/orchestrator launches and is rejected by Spawn.
func effectiveHarness(explicit domain.AgentHarness, kind domain.SessionKind, cfg domain.ProjectConfig) domain.AgentHarness {
	return effectiveHarnessFor(explicit, kind, "", cfg)
}

// effectiveHarnessFor is effectiveHarness for a session with a special role.
func effectiveHarnessFor(explicit domain.AgentHarness, kind domain.SessionKind, role domain.SessionRole, cfg domain.ProjectConfig) domain.AgentHarness {
	if explicit != "" {
		return explicit
	}
	if role := roleOverrideFor(kind, role, cfg).Harness; role != "" {
		return role
	}
	return ""
}

func roleConfigName(kind domain.SessionKind) string {
	if kind == domain.KindOrchestrator {
		return "orchestrator"
	}
	return "worker"
}

// harnessLookupError explains why no adapter drives a stored session's
// harness: a removed harness gets the read-only explanation, anything else the
// generic unknown-harness error.
func harnessLookupError(harness domain.AgentHarness) error {
	if harness.IsUnsupported() {
		return fmt.Errorf("%w: %s", ErrUnsupportedHarness, domain.UnsupportedHarnessMessage(harness))
	}
	return fmt.Errorf("%w: %s", ErrUnknownHarness, harness)
}

var defaultHarnessValue atomic.Value // domain.AgentHarness

// SetDefaultHarness records the daemon's default agent (TALLPINE_AGENT), the
// owner of the project base config under an unpinned role.
func SetDefaultHarness(h domain.AgentHarness) { defaultHarnessValue.Store(h) }

func launchDefaultHarness() domain.AgentHarness {
	if h, ok := defaultHarnessValue.Load().(domain.AgentHarness); ok && h != "" {
		return h
	}
	return domain.FallbackHarness
}

// effectiveAgentConfig merges the role override's agent config over the
// project's base agent config; set override fields win.
//
// Model/Mode are inherited only when the launch harness matches the role's
// configured harness — otherwise they were tuned for a different agent and
// would leak a provider-specific alias onto the wrong harness. An empty role
// harness means "not pinned" and always matches. Permissions is
// harness-neutral and is always inherited.
//
// When the role's configured harness is one this build no longer ships, the
// project-level Model/Effort/Mode were chosen for that harness too, so they are
// dropped as well instead of being handed to its fallback. The same holds when
// the launch harness differs from the role's pinned one (e.g. `spawn --agent
// codex` under a Claude-tuned project base): the base is dropped, Permissions kept.
func effectiveAgentConfig(harness domain.AgentHarness, kind domain.SessionKind, cfg domain.ProjectConfig) ports.AgentConfig {
	return effectiveAgentConfigFor(harness, kind, "", cfg)
}

// effectiveAgentConfigFor is effectiveAgentConfig for a session with a special
// role: a planner resolves through roleOverrideFor.
func effectiveAgentConfigFor(harness domain.AgentHarness, kind domain.SessionKind, sessionRole domain.SessionRole, cfg domain.ProjectConfig) ports.AgentConfig {
	return effectiveAgentConfigDefault(harness, kind, sessionRole, cfg, launchDefaultHarness())
}

// effectiveAgentConfigDefault is effectiveAgentConfigFor with the default agent
// passed in, which owns the project base under an unpinned role.
func effectiveAgentConfigDefault(harness domain.AgentHarness, kind domain.SessionKind, sessionRole domain.SessionRole, cfg domain.ProjectConfig, defaultAgent domain.AgentHarness) ports.AgentConfig {
	merged := cfg.AgentConfig
	role := roleOverrideFor(kind, sessionRole, cfg)
	// The project base was tuned for the harness the role resolves to without an
	// explicit --agent: its pinned harness, else the daemon's default agent.
	baseHarness := role.Harness
	if baseHarness == "" {
		baseHarness = defaultAgent
	}
	if role.Harness.IsUnsupported() || (harness != "" && baseHarness != harness) {
		merged.Model, merged.Effort, merged.Mode = "", "", ""
	}
	override := role.AgentConfig
	harnessMatches := role.Harness == "" || role.Harness == harness
	if harnessMatches && override.Model != "" {
		merged.Model = override.Model
	}
	if harnessMatches && override.Effort != "" {
		merged.Effort = override.Effort
	}
	if harnessMatches && override.Mode != "" {
		merged.Mode = override.Mode
	}
	if override.Permissions != "" {
		merged.Permissions = override.Permissions
	}
	return merged
}

// EffectivePermissions is the permission mode a fresh session of kind would
// launch with under cfg: role override, then project agentConfig, then auto.
func EffectivePermissions(kind domain.SessionKind, cfg domain.ProjectConfig) domain.PermissionMode {
	return applySpawnAgentConfig(effectiveAgentConfig("", kind, cfg), ports.AgentConfig{}).Permissions
}

// launchOptionsStore keeps a terminal session's launch choices; optional.
type launchOptionsStore interface {
	SessionLaunchOptions(ctx context.Context, id domain.SessionID) (domain.AgentLaunchOptions, error)
	SetSessionLaunchOptions(ctx context.Context, id domain.SessionID, options domain.AgentLaunchOptions) error
	UpdateSessionWithLaunchOptions(ctx context.Context, rec domain.SessionRecord, options domain.AgentLaunchOptions) error
}

// sessionLaunchOptions reads the saved launch choices; none on a failed read.
func (m *Manager) sessionLaunchOptions(ctx context.Context, id domain.SessionID) domain.AgentLaunchOptions {
	store, ok := m.store.(launchOptionsStore)
	if !ok {
		return domain.AgentLaunchOptions{}
	}
	options, err := store.SessionLaunchOptions(ctx, id)
	if err != nil {
		m.logger.Warn("read session launch options", "session", id, "error", err)
		return domain.AgentLaunchOptions{}
	}
	return options
}

// launchOptionsWithWindow reads the session's launch options and, for a percent
// compaction window, adds the model's context window from the saved catalog so
// the adapter can turn the percent into tokens. An empty model resolves to the
// catalog's default model, else its largest window; a named model missing from
// the catalog stays 0, so the percent is then not applied.
func (m *Manager) launchOptionsWithWindow(ctx context.Context, rec domain.SessionRecord, project domain.ProjectConfig, model string) domain.AgentLaunchOptions {
	return m.freshLaunchOptions(ctx, rec.Harness, rec.Kind, rec.ProjectID, project, model, m.sessionLaunchOptions(ctx, rec.ID))
}

// withModelWindow adds the model's context window from the saved catalog to a
// percent compaction window's options. An empty model uses the catalog default,
// else its largest window; a named model the catalog lacks stays 0.
func (m *Manager) withModelWindow(ctx context.Context, harness domain.AgentHarness, project domain.ProjectID, model string, options domain.AgentLaunchOptions) domain.AgentLaunchOptions {
	if !strings.HasSuffix(options.AutoCompactWindow, "%") {
		return options
	}
	options.ModelContextWindow, _ = contextwindow.Lookup(ctx, m.modelCatalog, string(harness), string(project), model)
	return options
}

// freshLaunchOptions applies the Settings compaction default and the model
// window to a fresh terminal spawn, which never reads stored launch options.
//
// A session with no window of its own takes domain.DefaultAutoCompactChoice:
// the Settings default for its agent, and for an orchestrator the project's
// orchestrator window or Tallpine's built-in one.
func (m *Manager) freshLaunchOptions(ctx context.Context, harness domain.AgentHarness, kind domain.SessionKind, projectID domain.ProjectID, project domain.ProjectConfig, model string, options domain.AgentLaunchOptions) domain.AgentLaunchOptions {
	if options.AutoCompactWindow == "" {
		var harnessDefault string
		if d, ok := m.defaults.(interface {
			AutoCompactDefault(context.Context, domain.AgentHarness) string
		}); ok {
			harnessDefault = d.AutoCompactDefault(ctx, harness)
		}
		envWindow := harness == domain.HarnessClaudeCode && project.Env[contextwindow.ClaudeAutoCompactWindowEnv] != ""
		options.AutoCompactWindow = domain.DefaultAutoCompactChoice(kind, project, harnessDefault, envWindow)
	}
	options.EnabledPlugins, options.AutoMemoryOff = project.ClaudeLaunchFor(domain.SessionRoleFor(kind))
	options.ShellOutputChars = domain.ShellOutputLimit(project.Env)
	return m.withModelWindow(ctx, harness, projectID, model, options)
}

func restoredAgentConfig(rec domain.SessionRecord, cfg domain.ProjectConfig) ports.AgentConfig {
	merged := effectiveAgentConfigFor(rec.Harness, rec.Kind, rec.Role, cfg)
	// Model and effort are durable session choices for every harness. Empty
	// values mean provider defaults, not today's project or role defaults.
	merged.Model = rec.Metadata.Model
	merged.Effort = rec.Metadata.Effort
	if rec.Metadata.Permissions != "" {
		merged.Permissions = rec.Metadata.Permissions
	}
	return merged
}

func applySpawnAgentConfig(base, override ports.AgentConfig) ports.AgentConfig {
	if override.Model != "" {
		base.Model = override.Model
	}
	if override.Effort != "" {
		base.Effort = override.Effort
	}
	if override.Mode != "" {
		base.Mode = override.Mode
	}
	if override.Permissions != "" {
		base.Permissions = override.Permissions
	}
	if base.Permissions == "" {
		base.Permissions = ports.PermissionModeAuto
	}
	return base
}

// normalizeAgentConfigForHarness returns a copy of cfg with any effective
// Model value moved into Mode for adapters that expose selectable values as
// agent-owned modes rather than raw model ids. This keeps the spawn API's
// single `model` field usable while ensuring the adapter's launch command
// receives the value in the field it actually reads (e.g. Amp's `--mode` flag
// reads Config.Mode). The original cfg is left unchanged so callers can still
// persist the resolved Model separately.
func normalizeAgentConfigForHarness(harness domain.AgentHarness, cfg ports.AgentConfig) ports.AgentConfig {
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return cfg
	}
	catalog := modelcatalog.Base(string(harness))
	if catalog.SelectionMode != ports.ModelSelectionModeList {
		return cfg
	}
	out := cfg
	out.Mode = model
	out.Model = ""
	return out
}

// resolvedModelForMetadata returns the user-visible resolved model selection to
// persist on the session. Model-based adapters use Config.Model; mode-list
// adapters like Amp store the selection in Config.Mode. An explicit Model value
// always wins so spawn overrides are reported exactly as requested.
func resolvedModelForMetadata(harness domain.AgentHarness, effective, adapter ports.AgentConfig) string {
	if model := strings.TrimSpace(effective.Model); model != "" {
		return model
	}
	catalog := modelcatalog.Base(string(harness))
	if catalog.SelectionMode == ports.ModelSelectionModeList {
		return strings.TrimSpace(adapter.Mode)
	}
	return ""
}

// validateSpawnModel rejects unknown choices only when Tallpine owns a complete
// static catalog. Dynamic catalogs and direct-entry agents defer authoritative
// model validation to the selected agent at launch time.
func validateSpawnModel(harness domain.AgentHarness, model string) error {
	if modelcatalog.Accepts(string(harness), model) {
		return nil
	}
	return fmt.Errorf("model %q is not supported by harness %q", model, harness)
}

// plannerLock is the managed-policy hook for the planner's harness and model
// (the agent-policy `planner` rule). A lock wins over the project's own choice;
// it reports false when nothing is locked. The daemon sets it once at startup.
var plannerLock atomic.Pointer[func() (domain.RoleOverride, bool)]

// SetPlannerLock wires the managed planner lock (nil clears it).
func SetPlannerLock(lock func() (domain.RoleOverride, bool)) {
	if lock == nil {
		plannerLock.Store(nil)
		return
	}
	plannerLock.Store(&lock)
}

func plannerModelLock() (domain.RoleOverride, bool) {
	if f := plannerLock.Load(); f != nil {
		return (*f)()
	}
	return domain.RoleOverride{}, false
}

// roleOverrideFor is roleOverride for a session with a special role. A planner
// takes the policy lock, else the project's planner override (ProjectConfig.Planner), else the
// orchestrator's harness and model, else the worker's. Never a fixed vendor or
// "strongest". Inherited overrides carry harness/model/effort/mode only: the
// planner never inherits the orchestrator's permissions.
func roleOverrideFor(kind domain.SessionKind, role domain.SessionRole, cfg domain.ProjectConfig) domain.RoleOverride {
	if role != domain.SessionRolePlanner {
		return roleOverride(kind, cfg)
	}
	if locked, ok := plannerModelLock(); ok {
		return locked
	}
	if p := cfg.Planner; p.Harness != "" || p.AgentConfig.Model != "" {
		p.AutoCompactWindow, p.AgentConfig.Permissions = "", ""
		return p
	}
	if o := cfg.Orchestrator; o.Harness != "" || o.AgentConfig.Model != "" {
		o.AutoCompactWindow, o.AgentConfig.Permissions = "", ""
		return o
	}
	return cfg.Worker
}

func roleOverride(kind domain.SessionKind, cfg domain.ProjectConfig) domain.RoleOverride {
	if kind == domain.KindOrchestrator {
		return cfg.Orchestrator
	}
	return cfg.Worker
}

// sessionPrefix returns the display prefix for a project: the explicit
// SessionPrefix when set, otherwise the first 12 characters of the project ID.
func sessionPrefix(project domain.ProjectRecord) string {
	if p := strings.TrimSpace(project.Config.SessionPrefix); p != "" {
		return p
	}
	if len(project.ID) <= 12 {
		return project.ID
	}
	return project.ID[:12]
}
