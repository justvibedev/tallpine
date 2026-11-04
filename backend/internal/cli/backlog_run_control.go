package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func newBacklogRunControlCommand(ctx *commandContext, name, short string) *cobra.Command {
	var project, reason, resumeAt string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   name + " <run>",
		Short: short,
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := ctx.backlogRunPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			body := apitypes.BacklogRunPauseRequest{}
			if name == "pause" {
				body.Reason = reason
				if resumeAt != "" {
					t, err := time.Parse(time.RFC3339, resumeAt)
					if err != nil {
						return usageError{fmt.Errorf("--resume-at must be RFC 3339, for example 2026-10-28T15:00:00Z")}
					}
					body.ResumeAt = &t
				}
			}
			var response backlogRunEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), backlogRunRefPath(base, args[0])+"/"+name, body, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", response.Run.Ref, response.Run.Status)
			return err
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	if name == "pause" {
		cmd.Flags().StringVar(&reason, "reason", "", "Why, for example the Claude 5-hour usage limit")
		cmd.Flags().StringVar(&resumeAt, "resume-at", "", "When the limit resets (RFC 3339), shown on the run card")
	}
	return cmd
}
