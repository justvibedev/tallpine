package settingsreg

import (
	"strings"

	"encoding/json"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/cleanup"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
)

// appSettings declares the install-wide preferences stored in app_settings
// (GET /settings and its PATCH routes). Defaults, bounds and validators come
// from their owners. Only the user may change them today.
func appSettings() []Setting {
	user := []Actor{ActorUser}
	hibernation := domain.DefaultChatHibernationPolicy()
	idleMin, idleMax := bound(int64(domain.MinChatHibernationIdle/time.Second)), bound(int64(domain.MaxChatHibernationIdle/time.Second))
	return []Setting{
		{Key: "app.defaultSessionMode", Scope: ScopeApp, Kind: KindEnum, Enum: []string{string(domain.SessionModeChat), string(domain.SessionModeTUI)},
			Default: jsonOf(domain.SessionModeChat), Description: "Interface new sessions open in.", Writers: user},
		{Key: "app.harnessDefaults", Scope: ScopeApp, Kind: KindObject, Default: jsonOf(map[string]any{}), Validate: validateHarnessDefaults,
			Description: "Per-harness defaults: disabled models and auto-compact window.", Writers: user},
		{Key: "app.chatHibernation.enabled", Scope: ScopeApp, Kind: KindBool, Default: jsonOf(hibernation.Enabled),
			Description: "Idle Chat sessions stop their agent processes and resume on the next message.", Writers: user},
		{Key: "app.chatHibernation.workerIdleSeconds", Scope: ScopeApp, Kind: KindInt, Default: jsonOf(int64(hibernation.WorkerIdle / time.Second)), Min: idleMin, Max: idleMax,
			Description: "Idle time before a worker Chat session hibernates.", Writers: user},
		{Key: "app.chatHibernation.orchestratorIdleSeconds", Scope: ScopeApp, Kind: KindInt, Default: jsonOf(int64(hibernation.OrchestratorIdle / time.Second)), Min: idleMin, Max: idleMax,
			Description: "Idle time before an orchestrator Chat session hibernates.", Writers: user},
		{Key: "app.versionRefresh", Scope: ScopeApp, Kind: KindBool, Default: jsonOf(true), Env: []string{"TALLPINE_VERSION_REFRESH"}, EnvOverride: killSwitch,
			Description: "After a harness update, idle open sessions restart on the new release.", Writers: user},
		{Key: "app.mobile.enabled", Scope: ScopeApp, Kind: KindBool, Default: jsonOf(true),
			Description: "Whether phones may connect at all. A managed value of false turns the mobile bridge off and no user or project setting can turn it back on; the transport is always encrypted.", Writers: user, MDMLockable: true},
		{Key: "app.dynamicOrchestratorContext", Scope: ScopeApp, Kind: KindBool, Default: jsonOf(true),
			Description: "Let an orchestrator raise its compaction window while it researches a large codebase itself (`tallpine orchestrator research on`). Off keeps every orchestrator on its early coordination window.", Writers: user, MDMLockable: true},
		{Key: "app.workflowDefaults.autoReview", Scope: ScopeApp, Kind: KindBool, Default: jsonOf(false),
			Description: "Start a native review when a worker opens a PR, in every project that does not set its own value.", Writers: user},
		{Key: "app.workflowDefaults.planningMode", Scope: ScopeApp, Kind: KindEnum, Enum: []string{string(domain.PlanningAuto), string(domain.PlanningAlways), string(domain.PlanningOff)}, Default: jsonOf(string(domain.PlanningAuto)),
			Description: "When a project plans before it builds, in every project that does not set its own mode. A managed policy.planner.mode wins.", Writers: user},
		{Key: "app.workflowDefaults.planAutoApprove", Scope: ScopeApp, Kind: KindBool, Default: jsonOf(false),
			Description: "Approve submitted plans without the owner in every project that does not set its own value. On means plans go ahead without you; a managed policy.planner.autoApprove of off locks it off.", Writers: user},
		{Key: "app.storageCompaction", Scope: ScopeApp, Kind: KindBool, Default: jsonOf(true),
			Description: "Compact activity writes and expire the provider-event archive.", Writers: user},
		{Key: "app.providerEventRetentionDays", Scope: ScopeApp, Kind: KindInt, Default: jsonOf(settingssvc.DefaultProviderEventRetentionDays),
			Min: bound(settingssvc.MinProviderEventRetentionDays), Max: bound(settingssvc.MaxProviderEventRetentionDays),
			Description: "Days provider events of a terminated session are kept while compaction is on.", Writers: user, MDMLockable: true},
	}
}

// validateHarnessDefaults checks every harness entry with the settings
// service's own validator.
func validateHarnessDefaults(raw json.RawMessage) error {
	var byHarness map[string]domain.HarnessDefault
	if err := json.Unmarshal(raw, &byHarness); err != nil || byHarness == nil {
		return errors.New("must be an object of harness defaults")
	}
	for harness, value := range byHarness {
		if _, err := settingssvc.NormalizeHarnessDefault(domain.AgentHarness(harness), value); err != nil {
			return err
		}
	}
	return nil
}

// cleanupDescriptions documents the top-level fields of the cleanup policy.
var cleanupDescriptions = []struct{ name, desc string }{
	{"automatic", "Run storage cleanup automatically."},
	{"worktrees", "Cleanup of merged or old session worktrees."},
	{"artifacts", "Retention of session artifacts."},
	{"sessionTemp", "Cleanup of session scratch directories."},
	{"harnessCaches", "Retention of harness caches."},
	{"systemTemp", "Cleanup of Tallpine-created system temp files."},
	{"goBuildCache", "Trimming of the Go build cache."},
	{"containers", "Removal of session-labelled containers."},
	{"branches", "Cleanup of merged session branches."},
	{"backlog", "Expiry of backlog inbox items."},
}

// cleanupSettings declares the top-level fields of the storage cleanup policy
// (PATCH /cleanup/policy). Defaults come from cleanup.DefaultPolicy and values
// are validated by applying them as a patch, as the route does.
func cleanupSettings() []Setting {
	defaults := map[string]json.RawMessage{}
	_ = json.Unmarshal(jsonOf(cleanup.DefaultPolicy()), &defaults)
	out := make([]Setting, 0, len(cleanupDescriptions))
	for _, c := range cleanupDescriptions {
		kind := KindObject
		if c.name == "automatic" {
			kind = KindBool
		}
		name := c.name
		out = append(out, Setting{
			Key: Key("app.cleanup." + name), Scope: ScopeApp, Kind: kind, Default: defaults[name],
			Description: c.desc, Writers: []Actor{ActorUser}, MDMLockable: true,
			Validate: func(raw json.RawMessage) error {
				patch, _ := json.Marshal(map[string]json.RawMessage{name: raw})
				_, err := cleanup.ApplyPatch(cleanup.DefaultPolicy(), patch)
				return err
			},
		})
	}
	return out
}

// killSwitch is the EnvOverride of a setting whose variable can only turn it
// off: "off" forces false; "on", empty or an invalid value leave it to the user
// (the daemon ANDs the variable with the stored toggle).
func killSwitch(text string) (json.RawMessage, bool) {
	if strings.TrimSpace(text) == "" {
		return nil, false
	}
	if on, err := config.ParseToggle(text); err == nil && !on {
		return json.RawMessage("false"), true
	}
	return nil, false
}
