package cli

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
)

func newOrchestratorIntentCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "intent",
		Short: "Keep owner intents and standing decisions that survive compaction and handoff",
		Long: "Durable notes for the project's orchestrator, stored in the daemon instead of chat history.\n" +
			"Examples: 'hold install until owner switches accounts', 'merge #N after #M', 'owner said X'.\n" +
			"Run `intent ls` after a compaction or handoff; clear an intent with `intent done` once it is resolved.",
	}
	cmd.AddCommand(newIntentAddCommand(ctx), newIntentListCommand(ctx), newIntentDoneCommand(ctx))
	return cmd
}

func intentsPath(ctx *commandContext, cmd *cobra.Command, project string) (string, error) {
	resolved, err := ctx.resolveSpawnProject(cmd.Context(), project)
	if err != nil {
		return "", err
	}
	return "projects/" + url.PathEscape(resolved.ID) + "/intents", nil
}

func newIntentAddCommand(ctx *commandContext) *cobra.Command {
	var project string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "add <text>",
		Short: "Record an intent or standing decision",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := intentsPath(ctx, cmd, project)
			if err != nil {
				return err
			}
			var res controllers.IntentResponse
			if err := ctx.postJSON(cmd.Context(), base, controllers.CreateIntentRequest{Text: strings.Join(args, " ")}, &res); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Recorded intent %s\n", res.ID)
			return err
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	return cmd
}

func newIntentListCommand(ctx *commandContext) *cobra.Command {
	var project string
	var jsonOutput, all bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List open intents (--all includes cleared ones)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			base, err := intentsPath(ctx, cmd, project)
			if err != nil {
				return err
			}
			if all {
				base += "?all=true"
			}
			var res controllers.ListIntentsResponse
			if err := ctx.getJSON(cmd.Context(), base, &res); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeIntents(cmd, res.Intents)
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	cmd.Flags().BoolVar(&all, "all", false, "Include cleared intents")
	return cmd
}

func writeIntents(cmd *cobra.Command, items []controllers.IntentResponse) error {
	out := cmd.OutOrStdout()
	if len(items) == 0 {
		_, err := fmt.Fprintln(out, "(no open intents)")
		return err
	}
	for _, in := range items {
		mark := ""
		if in.DoneAt != nil {
			mark = " [done]"
		}
		if _, err := fmt.Fprintf(out, "%s%s  %s  %s\n", in.ID, mark, in.CreatedAt.Format("2006-01-02"), in.Text); err != nil {
			return err
		}
	}
	return nil
}

func newIntentDoneCommand(ctx *commandContext) *cobra.Command {
	var project string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "done <id>",
		Short: "Clear an intent that is resolved",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := intentsPath(ctx, cmd, project)
			if err != nil {
				return err
			}
			if err := ctx.postJSON(cmd.Context(), base+"/"+url.PathEscape(args[0])+"/done", struct{}{}, nil); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Cleared intent %s\n", args[0])
			return err
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	return cmd
}
