package cli

import (
	"bufio"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

// confirmBacklogDelete asks a y/N question; anything but y/yes aborts.
func confirmBacklogDelete(cmd *cobra.Command, question string) (bool, error) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N] ", question)
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && line == "" {
		return false, usageError{errors.New("no confirmation on stdin; pass --yes to skip the prompt")}
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func newBacklogDeleteCommand(ctx *commandContext) *cobra.Command {
	var project string
	var jsonOutput, yes bool
	cmd := &cobra.Command{
		Use:   "delete <ref>",
		Short: "Permanently delete a done or dropped item (and its closed children)",
		Long:  "Hard-deletes a closed item, its links, and its closed children. Open and inbox items are refused, as is a parent with any open child. Refs are never reused.",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := ctx.backlogPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			if !yes {
				ok, err := confirmBacklogDelete(cmd, fmt.Sprintf("Permanently delete %s? This cannot be undone.", args[0]))
				if err != nil {
					return err
				}
				if !ok {
					_, err := fmt.Fprintln(cmd.OutOrStdout(), "aborted")
					return err
				}
			}
			var response apitypes.BacklogDeleteResponse
			if err := ctx.deleteJSON(cmd.Context(), backlogItemPath(base, args[0]), &response); err != nil {
				return err
			}
			return printBacklogDeleted(cmd, response, jsonOutput, false)
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

func newBacklogPurgeCommand(ctx *commandContext) *cobra.Command {
	var project, olderThan string
	var done, dropped, dryRun, jsonOutput, yes bool
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Permanently delete all done and/or dropped items",
		Long:  "Hard-deletes closed items in bulk. Pick --done and/or --dropped; --older-than keeps recent ones. A parent with an open child is skipped. Use --dry-run to list what would go.",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			req := apitypes.BacklogPurgeRequest{}
			if done {
				req.States = append(req.States, "done")
			}
			if dropped {
				req.States = append(req.States, "dropped")
			}
			if len(req.States) == 0 {
				return usageError{errors.New("give --done and/or --dropped")}
			}
			if olderThan != "" {
				days, err := parseBacklogAge(olderThan)
				if err != nil {
					return err
				}
				req.OlderThanDays = &days
			}
			base, err := ctx.backlogPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			if dryRun {
				req.DryRun = true
				var preview apitypes.BacklogDeleteResponse
				if err := ctx.postJSON(cmd.Context(), base+"/purge", req, &preview); err != nil {
					return err
				}
				return printBacklogDeleted(cmd, preview, jsonOutput, true)
			}
			if !yes {
				req.DryRun = true
				var preview apitypes.BacklogDeleteResponse
				if err := ctx.postJSON(cmd.Context(), base+"/purge", req, &preview); err != nil {
					return err
				}
				if len(preview.Deleted) == 0 {
					return printBacklogDeleted(cmd, preview, jsonOutput, true)
				}
				ok, err := confirmBacklogDelete(cmd, fmt.Sprintf("Permanently delete %d item(s): %s? This cannot be undone.", len(preview.Deleted), strings.Join(preview.Deleted, ", ")))
				if err != nil {
					return err
				}
				if !ok {
					_, err := fmt.Fprintln(cmd.OutOrStdout(), "aborted")
					return err
				}
				req.DryRun = false
			}
			var response apitypes.BacklogDeleteResponse
			if err := ctx.postJSON(cmd.Context(), base+"/purge", req, &response); err != nil {
				return err
			}
			return printBacklogDeleted(cmd, response, jsonOutput, false)
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	cmd.Flags().BoolVar(&done, "done", false, "Purge done items")
	cmd.Flags().BoolVar(&dropped, "dropped", false, "Purge dropped items")
	cmd.Flags().StringVar(&olderThan, "older-than", "", "Only items closed more than this long ago, in days (for example 30d)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "List what would be deleted without deleting")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

// parseBacklogAge reads "30d" or "30" as a number of days.
func parseBacklogAge(s string) (int, error) {
	days, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(s), "d"))
	if err != nil || days < 0 {
		return 0, usageError{fmt.Errorf("--older-than %q must be a number of days like 30d", s)}
	}
	return days, nil
}

func printBacklogDeleted(cmd *cobra.Command, response apitypes.BacklogDeleteResponse, jsonOutput, dryRun bool) error {
	if jsonOutput {
		return writeJSON(cmd.OutOrStdout(), response)
	}
	verb := "deleted"
	if dryRun {
		verb = "would delete"
	}
	out := cmd.OutOrStdout()
	if len(response.Deleted) == 0 {
		_, _ = fmt.Fprintln(out, "nothing to delete")
	} else {
		_, _ = fmt.Fprintf(out, "%s %d item(s): %s\n", verb, len(response.Deleted), strings.Join(response.Deleted, ", "))
	}
	if len(response.Skipped) > 0 {
		_, _ = fmt.Fprintf(out, "skipped %s (a child is still open)\n", strings.Join(response.Skipped, ", "))
	}
	return nil
}
