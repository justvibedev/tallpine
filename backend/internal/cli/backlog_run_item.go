package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func newBacklogRunItemCommand(ctx *commandContext) *cobra.Command {
	var project, status, reason, question string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "item <run> <item> --status queued|active|in_review|needs_you|landed|held|failed",
		Short: "Record one planned item's progress",
		Long: "Record progress. active needs a free slot (at most 3 items are active per project) and a running run. " +
			"needs_you needs --question: one specific question for the owner. held needs --reason. A second failed holds the item.",
		Args: usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if status == "" {
				return usageError{errors.New("--status is required")}
			}
			base, err := ctx.backlogRunPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			var response backlogRunEnvelopeDTO
			path := backlogRunRefPath(base, args[0]) + "/items/" + url.PathEscape(strings.TrimSpace(args[1]))
			if err := ctx.postJSON(cmd.Context(), path, apitypes.BacklogRunItemRequest{Status: status, Reason: reason, Question: question}, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s -> %s (run %s)\n", args[0], args[1], status, response.Run.Status)
			return err
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	f := cmd.Flags()
	f.StringVar(&status, "status", "", "New status")
	f.StringVar(&reason, "reason", "", "Why it is held or failed")
	f.StringVar(&question, "question", "", "The one question for the owner (needs_you)")
	return cmd
}

type backlogRunFinishDTO struct {
	Summary string `json:"summary"`
}

func newBacklogRunFinishCommand(ctx *commandContext) *cobra.Command {
	var project, summary, summaryFile string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "finish <run> (--summary <text> | --summary-file <file|->)",
		Short: "End the run with your summary (landed / waiting on the owner / held)",
		Long:  "End the run. Refuses while an item is still active or in review. The page adds cost and elapsed time per item from the usage ledgers; write the narrative only.",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := summary
			if summaryFile != "" {
				var raw []byte
				var err error
				if summaryFile == "-" {
					raw, err = io.ReadAll(cmd.InOrStdin())
				} else {
					raw, err = os.ReadFile(summaryFile)
				}
				if err != nil {
					return err
				}
				text = string(raw)
			}
			if strings.TrimSpace(text) == "" {
				return usageError{errors.New("a summary is required")}
			}
			base, err := ctx.backlogRunPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			var response backlogRunEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), backlogRunRefPath(base, args[0])+"/finish", backlogRunFinishDTO{Summary: text}, &response); err != nil {
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
	cmd.Flags().StringVar(&summary, "summary", "", "Summary text")
	cmd.Flags().StringVar(&summaryFile, "summary-file", "", "Read the summary from a file, or - for stdin")
	return cmd
}
