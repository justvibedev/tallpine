package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

// settingScope is the project to send for key: app.* keys never take one,
// project.* keys require one.
func settingScope(key, project string) (string, error) {
	switch {
	case strings.HasPrefix(key, "app."):
		return "", nil
	case strings.HasPrefix(key, "project.") && project == "":
		return "", fmt.Errorf("%s is a project setting; pass --project", key)
	}
	return project, nil
}

// parseSettingValue reads arg as JSON, falling back to a plain string so
// `settings set project.agentRules "Be brief."` needs no extra quoting.
func parseSettingValue(arg string) any {
	var v any
	if err := json.Unmarshal([]byte(arg), &v); err == nil {
		return v
	}
	return arg
}

func applySettingsPatch(ctx *commandContext, cmd *cobra.Command, patch apitypes.UpdateSettingsValuesRequest, key string, asJSON bool) error {
	var resp apitypes.SettingsValuesResponse
	if err := ctx.patchJSON(cmd.Context(), "settings/values", patch, &resp); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for _, v := range resp.Values {
		if v.Key != key {
			continue
		}
		if asJSON {
			return writeJSON(out, v)
		}
		value, _ := json.Marshal(v.Value)
		_, err := fmt.Fprintf(out, "%s = %s %s\n", v.Key, shortenValue(string(value)), describeSetting(v))
		return err
	}
	return nil
}

func newSettingsSetCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	var project string
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Change one setting",
		Long: "Sets a setting by key. The value is read as JSON (true, 30, \"text\", {...}); anything that is not " +
			"valid JSON is taken as a string. project.* keys need --project; policy.* keys change the install-wide " +
			"policy, or with --project that project's policy. Locked settings cannot be changed.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := settingScope(args[0], project)
			if err != nil {
				return err
			}
			patch := apitypes.UpdateSettingsValuesRequest{Project: scope, Set: map[string]any{args[0]: parseSettingValue(args[1])}}
			return applySettingsPatch(ctx, cmd, patch, args[0], asJSON)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Change the setting for this project")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

func newSettingsUnsetCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	var project string
	cmd := &cobra.Command{
		Use:   "unset <key>",
		Short: "Reset one setting to its default",
		Long: "Clears a setting so its default applies again. project.* keys need --project. policy.* keys drop " +
			"the stored value of that layer (with --project, the project's override) so it inherits again.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := settingScope(args[0], project)
			if err != nil {
				return err
			}
			return applySettingsPatch(ctx, cmd, apitypes.UpdateSettingsValuesRequest{Project: scope, Unset: []string{args[0]}}, args[0], asJSON)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Reset the setting for this project")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}
