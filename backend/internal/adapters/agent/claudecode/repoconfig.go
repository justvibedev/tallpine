package claudecode

import (
	"runtime"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/hooksjson"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/repoagentconfig"
)

// claudeRepoConfigArgs decides whether Claude may load agent config committed
// to the repository it runs in (project .claude/settings.json hooks and
// permissions, .mcp.json servers). A reviewer (skip) reads untrusted PR heads
// and never loads it, and takes no MCP servers beyond what Tallpine passes. A worker
// loads it unless the repoAgentConfig policy is off for its project, the
// managed policy forces it off, or a permission cap or managed policy is active. Either way Claude keeps the user's own settings
// (--setting-sources user).
func claudeRepoConfigArgs(skip bool, decision agentpolicy.Decision) []string {
	if skip {
		return []string{"--setting-sources", "user", "--strict-mcp-config"}
	}
	if !repoagentconfig.Of(decision).Enabled() || policyRestrictsSessions(decision) {
		// Under a permission cap or a managed policy no repo config loads, whatever
		// the per-project toggle says: project and local settings are where an
		// agent (or a "don't ask again" click) adds allow rules that silence
		// prompts, so capped sessions get the user's own settings only.
		return []string{"--setting-sources", "user"}
	}
	return nil
}

// policyRestrictsSessions reports whether a permission cap or the protectState
// sandbox is active, or any
// policy field comes from the managed layer. With neither, local settings load
// as before.
func policyRestrictsSessions(decision agentpolicy.Decision) bool {
	// A sandboxed session must not load project or local settings: their hooks
	// run outside the sandbox and their sandbox arrays merge in and widen it.
	if permissioncap.Of(decision).ForbidsBypass() || protectStateApplies(decision, runtime.GOOS) {
		return true
	}
	for _, rule := range rules.All() {
		for _, field := range rule.Schema().Fields {
			if s := decision.Source(rule.Name(), field.Name); s == agentpolicy.SourceManaged || s == agentpolicy.SourceManagedProfile {
				return true
			}
		}
	}
	return false
}

// claudeHooksSettings is Tallpine's hooks in Claude's settings shape, delivered in the
// --settings flag layer instead of the worktree's settings.local.json. The
// agent can edit any file in its worktree, but not its own launch argv, and
// hooks from every settings layer run together, so editing or deleting a
// workspace file can no longer remove Tallpine's PermissionRequest or activity hooks.
func claudeHooksSettings() map[string][]hooksjson.MatcherGroup {
	out := map[string][]hooksjson.MatcherGroup{}
	for _, spec := range claudeManagedHooks {
		if spec.Command == "" {
			continue
		}
		groups := out[spec.Event]
		entry := hooksjson.HookEntry{Type: "command", Command: spec.Command, Timeout: claudeHookTimeout}
		groups = append(groups, hooksjson.MatcherGroup{Matcher: spec.Matcher, Hooks: []hooksjson.HookEntry{entry}})
		out[spec.Event] = groups
	}
	return out
}
