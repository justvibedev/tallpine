package cli

import (
	"fmt"
	"slices"

	"github.com/spf13/cobra"
)

// rulesFlags are the set-config flags the narrow PUT /projects/{id}/rules
// endpoint takes.
var rulesFlags = []string{"agent-rules", "orchestrator-rules", "orchestrator-compact-window", "orchestrator-idle-compact", "orchestrator-research-window"}

// onlyRulesFlags reports whether set-config was given at least one rules flag
// and no flag that changes anything else (output flags aside).
func onlyRulesFlags(changed func(string) bool) bool {
	hasRules := false
	for _, f := range rulesFlags {
		hasRules = hasRules || changed(f)
	}
	if !hasRules {
		return false
	}
	for _, f := range projectConfigFieldFlags {
		if changed(f) && !slices.Contains(rulesFlags, f) {
			return false
		}
	}
	return true
}

// rulesRequest is the body of PUT /projects/{id}/rules: only the rules flags
// that were passed, so an omitted one stays as stored.
func rulesRequest(changed func(string) bool, opts projectSetConfigOptions) map[string]string {
	body := map[string]string{}
	if changed("agent-rules") {
		body["agentRules"] = opts.agentRules
	}
	if changed("orchestrator-rules") {
		body["orchestratorRules"] = opts.orchestratorRules
	}
	if changed("orchestrator-compact-window") {
		body["orchestratorAutoCompactWindow"] = opts.orchestratorCompactWindow
	}
	if changed("orchestrator-idle-compact") {
		body["orchestratorIdleCompactAt"] = opts.orchestratorIdleCompact
	}
	if changed("orchestrator-research-window") {
		body["orchestratorResearchCompactWindow"] = opts.orchestratorResearchWindow
	}
	return body
}

func reportProjectConfig(cmd *cobra.Command, opts projectSetConfigOptions, res projectResult) error {
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), res)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "updated config for project %s\n", res.Project.ID)
	return err
}

// onlyGitHubUserFlag reports whether --github-user is the only config field
// flag given, so it can go through the settings route an orchestrator may use.
func onlyGitHubUserFlag(changed func(string) bool) bool {
	if !changed("github-user") {
		return false
	}
	for _, f := range projectConfigFieldFlags {
		if f != "github-user" && changed(f) {
			return false
		}
	}
	return true
}
