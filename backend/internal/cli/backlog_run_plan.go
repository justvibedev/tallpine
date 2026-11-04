package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

func newBacklogRunPlanCommand(ctx *commandContext) *cobra.Command {
	var project, file string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "plan <run> --file <plan.json|->",
		Short: "Save the run's plan (replaces the previous one)",
		Long: "Save the plan as JSON: {\"lanes\":[[{\"ref\":\"B12\",\"model\":\"opus\"},{\"ref\":\"B14\",\"model\":\"sonnet\"}],[{\"ref\":\"B13\",\"model\":\"haiku\"}]]}. " +
			"Lanes run in parallel, items inside a lane in order. A blocked item must follow its blocker in the same lane. Parents are not planned; plan their children. The first plan starts the run.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return usageError{errors.New("--file is required (use - for stdin)")}
			}
			var raw []byte
			var err error
			if file == "-" {
				raw, err = io.ReadAll(cmd.InOrStdin())
			} else {
				raw, err = os.ReadFile(file)
			}
			if err != nil {
				return err
			}
			var body json.RawMessage = raw
			if !json.Valid(raw) {
				return usageError{errors.New("the plan is not valid JSON")}
			}
			base, err := ctx.backlogRunPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			var response backlogRunEnvelopeDTO
			if err := ctx.putJSON(cmd.Context(), backlogRunRefPath(base, args[0])+"/plan", body, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "planned %s: %d items [%s]\n", response.Run.Ref, len(response.Run.Items), response.Run.Status)
			return err
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	cmd.Flags().StringVar(&file, "file", "", "Plan JSON file, or - for stdin")
	return cmd
}
