package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func newBacklogRunStartCommand(ctx *commandContext) *cobra.Command {
	var project, scope, parent string
	var all, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "start --scope local|pr|pr-merge (--all | --parent <ref> | <ref>...)",
		Short: "Create a run for all open items, some items, or one parent's children",
		Long:  "Create a run. Only one run is open per project. Name the publishing scope the owner chose for this run: local (commit in the worktree), pr (green and reviewed PR) or pr-merge (merged PR).",
		Args:  usageArgs(cobra.ArbitraryArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := apitypes.StartBacklogRunRequest{Scope: scope}
			switch {
			case all && parent == "" && len(args) == 0:
				body.Selection = "all"
			case !all && parent != "" && len(args) == 0:
				body.Selection, body.Refs = "parent", []string{parent}
			case !all && parent == "" && len(args) > 0:
				body.Selection, body.Refs = "refs", args
			default:
				return usageError{errors.New("choose exactly one of --all, --parent <ref> or a list of refs")}
			}
			if scope == "" {
				return usageError{errors.New("--scope is required: ask the owner once per run")}
			}
			base, err := ctx.backlogRunPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			var response backlogRunEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), base, body, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "started %s [%s, scope %s]\n", response.Run.Ref, response.Run.Status, response.Run.Scope)
			return err
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	cmd.Flags().StringVar(&scope, "scope", "", "Publishing scope for the run: local, pr or pr-merge")
	cmd.Flags().BoolVar(&all, "all", false, "Every open item")
	cmd.Flags().StringVar(&parent, "parent", "", "Run the children of this parent item")
	return cmd
}
