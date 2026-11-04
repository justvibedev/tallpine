package cli

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func newBacklogStateCommand(ctx *commandContext, name, short, state string) *cobra.Command {
	var project, reason string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   name + " <ref>",
		Short: short,
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := ctx.backlogPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			var response backlogItemEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), backlogItemPath(base, args[0])+"/state", apitypes.BacklogStateRequest{State: state, Reason: reason}, &response); err != nil {
				return err
			}
			return printBacklogMutation(cmd, response, jsonOutput, name)
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	if state == "done" || state == "dropped" {
		cmd.Flags().StringVar(&reason, "reason", "", "Why it closed")
	}
	return cmd
}

func newBacklogHoldCommand(ctx *commandContext) *cobra.Command {
	var project string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "hold <ref> <reason...>",
		Short: "Hold an item so next skips it",
		Args:  usageArgs(cobra.MinimumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.setBacklogHold(cmd, project, args[0], strings.Join(args[1:], " "), jsonOutput, "held")
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	return cmd
}

func newBacklogUnholdCommand(ctx *commandContext) *cobra.Command {
	var project string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "unhold <ref>",
		Short: "Remove an item's hold",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.setBacklogHold(cmd, project, args[0], "", jsonOutput, "unheld")
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	return cmd
}

func (c *commandContext) setBacklogHold(cmd *cobra.Command, project, ref, reason string, jsonOutput bool, verb string) error {
	base, err := c.backlogPath(cmd.Context(), project)
	if err != nil {
		return err
	}
	var response backlogItemEnvelopeDTO
	if err := c.patchJSON(cmd.Context(), backlogItemPath(base, ref), map[string]any{"hold": reason}, &response); err != nil {
		return err
	}
	return printBacklogMutation(cmd, response, jsonOutput, verb)
}

func newBacklogMoveCommand(ctx *commandContext) *cobra.Command {
	var project, before, after string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "move <ref>",
		Short: "Move an item before or after another in the drag order",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (before == "") == (after == "") {
				return usageError{errors.New("give exactly one of --before or --after")}
			}
			base, err := ctx.backlogPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			if err := ctx.postJSON(cmd.Context(), base+"/reorder", apitypes.BacklogReorderRequest{Ref: args[0], Before: before, After: after}, nil); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), map[string]string{"moved": args[0]})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "moved %s\n", args[0])
			return err
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	cmd.Flags().StringVar(&before, "before", "", "Place directly before this item")
	cmd.Flags().StringVar(&after, "after", "", "Place directly after this item")
	return cmd
}

// printBacklogMutation prints the changed item (JSON) or a one-line summary.
func printBacklogMutation(cmd *cobra.Command, response backlogItemEnvelopeDTO, jsonOutput bool, verb string) error {
	if jsonOutput {
		return writeJSON(cmd.OutOrStdout(), response)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s [%s] %s\n", verb, response.Item.Ref, response.Item.State, response.Item.Title)
	return err
}

func queryEscape(s string) string { return url.PathEscape(s) }

func errNotKeyValue(arg string) error { return fmt.Errorf("%q is not key=value", arg) }

var errConflictingHeldFlags = errors.New("--held and --unheld are mutually exclusive")
