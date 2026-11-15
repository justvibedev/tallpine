package cli

import (
	"fmt"
	"net/url"
	"strings"
	"time"

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
	var project, wakeAt string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "add <text>",
		Short: "Record an intent or standing decision",
		Long: "Record an intent. With --wake-at the daemon sends the orchestrator one message naming the intent at that time,\n" +
			"if it is still open. Delivery needs the project setting intentWake (off by default); a daemon that was down at\n" +
			"that time delivers it once on start.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wake, err := parseWakeAt(wakeAt, time.Now(), time.Local)
			if err != nil {
				return err
			}
			base, err := intentsPath(ctx, cmd, project)
			if err != nil {
				return err
			}
			var res controllers.IntentResponse
			if err := ctx.postJSON(cmd.Context(), base, controllers.CreateIntentRequest{Text: strings.Join(args, " "), WakeAt: wake}, &res); err != nil {
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
	cmd.Flags().StringVar(&wakeAt, "wake-at", "", "Wake the orchestrator once at this time if the intent is still open: RFC 3339, local \"2006-01-02 15:04\", or a duration from now such as 90m")
	return cmd
}

// parseWakeAt reads a --wake-at value: RFC 3339, a local date-time, or a
// duration from now. Empty means no wake.
func parseWakeAt(v string, now time.Time, loc *time.Location) (*time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	var t time.Time
	if d, err := time.ParseDuration(v); err == nil {
		t = now.Add(d)
	} else if t, err = time.Parse(time.RFC3339, v); err != nil {
		for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04"} {
			if t, err = time.ParseInLocation(layout, v, loc); err == nil {
				break
			}
		}
		if err != nil {
			return nil, fmt.Errorf("--wake-at %q: use RFC 3339, \"2006-01-02 15:04\" (local time) or a duration such as 90m", v)
		}
	}
	if !t.After(now) {
		return nil, fmt.Errorf("--wake-at %q is not in the future", v)
	}
	t = t.UTC()
	return &t, nil
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
		if in.WakeAt != nil {
			mark += " [wake " + in.WakeAt.Local().Format("2006-01-02 15:04") + "]"
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
