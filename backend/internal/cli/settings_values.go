package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func fetchSettingsValues(ctx *commandContext, cmd *cobra.Command, project string) (apitypes.SettingsValuesResponse, error) {
	path := "settings/values"
	if project != "" {
		path += "?" + url.Values{"project": {project}}.Encode()
	}
	var resp apitypes.SettingsValuesResponse
	err := ctx.getJSON(cmd.Context(), path, &resp)
	return resp, err
}

// describeSetting is "(source)" or "(source, locked by X)".
func describeSetting(v apitypes.SettingValue) string {
	if v.Locked {
		return fmt.Sprintf("(%s, locked by %s)", v.Source, v.LockedBy)
	}
	return "(" + v.Source + ")"
}

func newSettingsListCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	var project string
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List settings with their effective values",
		Long: "Lists every app-wide setting (app.*) and the install-wide agent policy (policy.*), or with --project " +
			"that project's settings (project.*) and the policy as it applies to it. Each line shows the value, the " +
			"layer it comes from (default, user, project, env or managed) and whether it is locked.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resp, err := fetchSettingsValues(ctx, cmd, project)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, resp)
			}
			for _, v := range resp.Values {
				value, _ := json.Marshal(v.Value)
				if _, err := fmt.Fprintf(out, "%s = %s %s\n", v.Key, shortenValue(string(value)), describeSetting(v)); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "List this project's settings instead of app-wide ones")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

func newSettingsGetCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	var project string
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Show one setting's effective value",
		Long: "Prints the effective value of one setting as JSON, followed by where it comes from and who may change " +
			"it. project.* keys need --project.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			if strings.HasPrefix(key, "project.") && project == "" {
				return fmt.Errorf("%s is a project setting; pass --project", key)
			}
			scope := project
			if strings.HasPrefix(key, "app.") {
				scope = "" // app settings have no project scope
			}
			resp, err := fetchSettingsValues(ctx, cmd, scope)
			if err != nil {
				return err
			}
			for _, v := range resp.Values {
				if v.Key != key {
					continue
				}
				out := cmd.OutOrStdout()
				if asJSON {
					return writeJSON(out, v)
				}
				value, _ := json.MarshalIndent(v.Value, "", "  ")
				writers := strings.Join(v.Writable, ", ")
				if writers == "" {
					writers = "nobody"
				}
				_, err := fmt.Fprintf(out, "%s\n%s; writable by %s\n", value, describeSetting(v), writers)
				return err
			}
			return fmt.Errorf("unknown setting %q; see `tallpine settings ls`", key)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Read the setting for this project")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}
