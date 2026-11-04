package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func newBacklogAddCommand(ctx *commandContext) *cobra.Command {
	var project, note, doneWhen, scope, hold, parent, agentNotes string
	var open, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "add <title|->",
		Short: "File a backlog item",
		Long:  "File a backlog item. With - the title is the first line of stdin and the rest is the note, so a heredoc keeps backticks and quotes literal. Agents file to the inbox; --open (orchestrator and humans) skips it.",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			title := args[0]
			if title == "-" {
				raw, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				first, rest, _ := strings.Cut(strings.TrimLeft(string(raw), "\n"), "\n")
				title = first
				if strings.TrimSpace(note) == "" {
					note = strings.TrimSpace(rest)
				}
			}
			if strings.TrimSpace(title) == "" {
				return usageError{errors.New("a title is required")}
			}
			base, err := ctx.backlogPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			var response backlogItemEnvelopeDTO
			body := apitypes.CreateBacklogRequest{Title: title, Note: note, DoneWhen: doneWhen, Scope: scope, Hold: hold, Parent: parent, AgentNotes: agentNotes, Open: open}
			if err := ctx.postJSON(cmd.Context(), base, body, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "added %s [%s] %s\n", response.Item.Ref, response.Item.State, response.Item.Title)
			return err
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	f := cmd.Flags()
	f.StringVar(&note, "note", "", "Markdown note")
	f.StringVar(&doneWhen, "done-when", "", "Acceptance criteria")
	f.StringVar(&scope, "scope", "", "Publishing scope override: local, pr or pr-merge")
	f.StringVar(&hold, "hold", "", "Hold reason (held items are skipped by next)")
	f.StringVar(&parent, "parent", "", "Parent item ref (one level of nesting only)")
	f.StringVar(&agentNotes, "agent-notes", "", "Tallpine notes")
	f.BoolVar(&open, "open", false, "Skip the inbox (orchestrator only; humans always create open items)")
	return cmd
}
