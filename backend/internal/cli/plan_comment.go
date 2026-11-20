package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

// newPlanCommentCommand relays an owner comment on the whole submitted plan (B831).
func newPlanCommentCommand(ctx *commandContext) *cobra.Command {
	var project, text, file string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "comment <ref> (--text <comment>|--file comment.md)",
		Short: "Comment on the whole submitted plan; its planner accepts it, revises the plan or asks follow-up questions",
		Long: "Comment on the whole submitted plan. The owner comments on the plan card; an orchestrator relays only what the owner typed in chat. " +
			"The planner answers the comment: it accepts it as is, revises and resubmits the plan, or asks follow-up questions on the card. " +
			"The plan cannot be approved while a comment waits for the planner.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := commentText(text, file)
			if err != nil {
				return err
			}
			if body == "" {
				return fmt.Errorf("give the comment with --text or --file")
			}
			path, err := ctx.planPath(cmd.Context(), project, args[0], "comment")
			if err != nil {
				return err
			}
			var out planEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), path, map[string]any{"text": body}, &out); err != nil {
				return err
			}
			return printPlan(cmd.OutOrStdout(), out.Plan, asJSON)
		},
	}
	addBacklogProjectFlag(cmd, &project, &asJSON)
	addCommentFlags(cmd, &text, &file, "The comment")
	return cmd
}

// newPlanReplanCommand supersedes the plan; an optional comment becomes the
// new planner run's instructions.
func newPlanReplanCommand(ctx *commandContext) *cobra.Command {
	var project, text, file string
	var asJSON bool
	cmd := &cobra.Command{
		Use: "replan <ref> [--comment <instructions>|--comment-file instructions.md]", Short: "Supersede the plan and plan again",
		Long: "Supersede the plan and plan again. A comment is passed to the new planner as its instructions and stored on the new plan.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			comment, err := commentText(text, file)
			if err != nil {
				return err
			}
			path, err := ctx.planPath(cmd.Context(), project, args[0], "replan")
			if err != nil {
				return err
			}
			var out planEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), path, map[string]any{"comment": comment}, &out); err != nil {
				return err
			}
			return printPlan(cmd.OutOrStdout(), out.Plan, asJSON)
		},
	}
	addBacklogProjectFlag(cmd, &project, &asJSON)
	cmd.Flags().StringVar(&text, "comment", "", "The owner's instructions for the new planner run")
	cmd.Flags().StringVar(&file, "comment-file", "", "Read the instructions from this file")
	return cmd
}

func addCommentFlags(cmd *cobra.Command, text, file *string, what string) {
	cmd.Flags().StringVar(text, "text", "", what)
	cmd.Flags().StringVar(file, "file", "", what+", read from this file")
}

// commentText is the comment from --text, or the file when given.
func commentText(text, file string) (string, error) {
	if file != "" {
		if text != "" {
			return "", fmt.Errorf("give the comment as text or as a file, not both")
		}
		body, err := readTextFile(file)
		return strings.TrimSpace(body), err
	}
	return strings.TrimSpace(text), nil
}

// writePlanComments renders the owner's comments for `plan show`.
func writePlanComments(b *strings.Builder, p apitypes.PlanView) {
	for _, c := range p.Comments {
		tags := []string{c.Outcome}
		if c.Approve {
			tags = append(tags, "with approve")
		}
		if c.Replan {
			tags = append(tags, "with replan")
		}
		fmt.Fprintf(b, "\nComment by %s (%s):\n  %s\n", c.By, strings.Join(tags, ", "), strings.ReplaceAll(c.Text, "\n", "\n  "))
	}
}
