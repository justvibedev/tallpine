package cli

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

// Hand-mirrored from controllers.SettingsChangesResponse.
type settingsChangesDTO struct {
	Changes []apitypes.SettingsChange `json:"changes"`
}

// maxLogValue shortens long values (rules text) in the table; --json keeps them whole.
const maxLogValue = 60

func newSettingsCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Inspect Tallpine settings",
		Long: "Reads the settings registry: every app, project and agent-policy setting with its effective value, " +
			"where the value comes from, whether managed config or the environment locks it, and the change log.",
	}
	cmd.AddCommand(newSettingsLogCommand(ctx), newSettingsListCommand(ctx), newSettingsGetCommand(ctx),
		newSettingsSetCommand(ctx), newSettingsUnsetCommand(ctx))
	return cmd
}

func newSettingsLogCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	var project string
	var limit int
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show recent settings changes and who made them",
		Long: "Shows the settings change log, newest first: when each setting changed, who changed it " +
			"(user, or the agent session) and its old and new values. Without --project it lists app-wide " +
			"settings (app.*, policy.*); with --project it lists that project's settings. Sensitive values " +
			"such as project env are redacted.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			query := url.Values{}
			if project != "" {
				query.Set("project", project)
			}
			if limit > 0 {
				query.Set("limit", strconv.Itoa(limit))
			}
			path := "settings/changes"
			if len(query) > 0 {
				path += "?" + query.Encode()
			}
			var resp settingsChangesDTO
			if err := ctx.getJSON(cmd.Context(), path, &resp); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, resp)
			}
			if len(resp.Changes) == 0 {
				_, err := fmt.Fprintln(out, "No settings changes recorded.")
				return err
			}
			for _, c := range resp.Changes {
				if _, err := fmt.Fprintf(out, "%s  %s  %s: %s -> %s\n", c.ChangedAt.UTC().Format(time.RFC3339), c.Actor, c.Key,
					shortenValue(c.OldValue), shortenValue(c.NewValue)); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "List this project's settings changes instead of app-wide ones")
	cmd.Flags().IntVar(&limit, "limit", 0, "Changes to show (1-500; default 50)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

func shortenValue(v string) string {
	r := []rune(v)
	if len(r) <= maxLogValue {
		return v
	}
	return string(r[:maxLogValue-1]) + "…"
}
