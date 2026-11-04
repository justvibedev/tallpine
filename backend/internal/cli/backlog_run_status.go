package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func newBacklogRunStatusCommand(ctx *commandContext) *cobra.Command {
	var project string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "status [<run>]",
		Short: "Show a run (default: the open run) with its plan and progress",
		Long:  "Show a run's state, lanes, every item's status, cost and elapsed time. Check it at the start of every turn while a run is open.",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := ctx.backlogRunPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			var ref string
			if len(args) == 1 {
				ref = args[0]
			} else {
				var list backlogRunListDTO
				if err := ctx.getJSON(cmd.Context(), base+"?open=1", &list); err != nil {
					return err
				}
				if len(list.Runs) == 0 {
					if jsonOutput {
						return writeJSON(cmd.OutOrStdout(), list)
					}
					_, err := io.WriteString(cmd.OutOrStdout(), "no open run\n")
					return err
				}
				ref = list.Runs[0].Ref
			}
			var response backlogRunEnvelopeDTO
			if err := ctx.getJSON(cmd.Context(), backlogRunRefPath(base, ref), &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			return writeBacklogRun(cmd.OutOrStdout(), response.Run)
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	return cmd
}

func writeBacklogRun(w io.Writer, r apitypes.BacklogRunResponse) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s] scope %s, %s", r.Ref, r.Status, r.Scope, r.Selection)
	if len(r.Refs) > 0 {
		fmt.Fprintf(&b, " %s", strings.Join(r.Refs, " "))
	}
	b.WriteString("\n")
	if r.PauseReason != "" {
		fmt.Fprintf(&b, "paused: %s\n", r.PauseReason)
	}
	lane := -1
	for _, it := range r.Items {
		if it.Lane != lane {
			lane = it.Lane
			fmt.Fprintf(&b, "lane %d\n", lane+1)
		}
		fmt.Fprintf(&b, "  %s %-9s %-7s %s", it.Ref, it.DisplayStatus, it.Model, it.Title)
		if it.Question != "" {
			fmt.Fprintf(&b, "  ? %s", it.Question)
		} else if it.Reason != "" {
			fmt.Fprintf(&b, "  (%s)", it.Reason)
		}
		b.WriteString("\n")
	}
	if len(r.Items) == 0 {
		b.WriteString("no plan yet\n")
	}
	if r.Usage.CostNanos != nil {
		fmt.Fprintf(&b, "cost $%.2f", float64(*r.Usage.CostNanos)/1e9)
		if !r.Usage.CostComplete {
			b.WriteString(" (partial: some usage is unpriced)")
		}
		b.WriteString("\n")
	}
	if r.Summary != "" {
		fmt.Fprintf(&b, "\n%s\n", r.Summary)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
