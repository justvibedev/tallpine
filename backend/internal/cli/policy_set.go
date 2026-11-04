package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

// policySwitches maps the `policy set` names onto their policy settings.
var policySwitches = map[string]string{
	"security-mode": "policy." + agentpolicy.SecurityModeRule + "." + agentpolicy.SecurityModeEnabled,
	"allow-docker":  "policy." + agentpolicy.SecurityModeRule + "." + agentpolicy.SecurityModeAllowDocker,
}

func newPolicySetCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	var project string
	cmd := &cobra.Command{
		Use:   "set <security-mode|allow-docker> <on|off>",
		Short: "Turn Security mode, or a project's Docker access, on or off",
		Long: "security-mode turns Security mode on or off for the install, or with --project for that project " +
			"(a project's off beats the install's on). allow-docker leaves the Docker socket reachable inside the " +
			"fence for one project (--project is required); it gives the agent full host access. Both need the " +
			"install token, with one exception: an orchestrator may turn security-mode on for its own project (--project <its project>). " +
			"Any other agent change (install-wide, turning it off, allow-docker, another project) is refused with AGENT_NOT_PERMITTED. " +
			"A managed or environment lock cannot be changed. New and restored sessions get the change; running " +
			"ones need a relaunch. `tallpine settings unset policy.securityMode.enabled` returns to the default.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, ok := policySwitches[args[0]]
			if !ok {
				return fmt.Errorf("unknown policy switch %q; use security-mode or allow-docker", args[0])
			}
			var value bool
			switch args[1] {
			case "on", "true":
				value = true
			case "off", "false":
				value = false
			default:
				return fmt.Errorf("value must be on or off, got %q", args[1])
			}
			if args[0] == "allow-docker" && project == "" {
				return fmt.Errorf("allow-docker is set per project; pass --project")
			}
			scope, err := settingScope(key, project)
			if err != nil {
				return err
			}
			patch := apitypes.UpdateSettingsValuesRequest{Project: scope, Set: map[string]any{key: value}}
			return applySettingsPatch(ctx, cmd, patch, key, asJSON)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Change the setting for this project")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}
