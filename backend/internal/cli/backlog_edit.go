package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"
)

func newBacklogEditCommand(ctx *commandContext) *cobra.Command {
	var project, title, note, doneWhen, scope, parent, agentNotes string
	var noParent, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "edit <ref>",
		Short: "Edit an item's title, note, done-when, scope, parent or agent notes",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{}
			f := cmd.Flags()
			for flag, value := range map[string]string{"title": title, "note": note, "done-when": doneWhen, "scope": scope, "agent-notes": agentNotes} {
				if f.Changed(flag) {
					body[strings.ReplaceAll(camel(flag), "-", "")] = value
				}
			}
			if f.Changed("parent") && noParent {
				return usageError{errors.New("--parent and --no-parent are mutually exclusive")}
			}
			if f.Changed("parent") {
				body["parent"] = parent
			}
			if noParent {
				body["parent"] = ""
			}
			if len(body) == 0 {
				return usageError{errors.New("nothing to change: pass at least one of --title, --note, --done-when, --scope, --parent, --no-parent, --agent-notes")}
			}
			base, err := ctx.backlogPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			var response backlogItemEnvelopeDTO
			if err := ctx.patchJSON(cmd.Context(), backlogItemPath(base, args[0]), body, &response); err != nil {
				return err
			}
			return printBacklogMutation(cmd, response, jsonOutput, "updated")
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	f := cmd.Flags()
	f.StringVar(&title, "title", "", "New title")
	f.StringVar(&note, "note", "", "New markdown note")
	f.StringVar(&doneWhen, "done-when", "", "New acceptance criteria")
	f.StringVar(&scope, "scope", "", "Publishing scope: local, pr, pr-merge (empty clears)")
	f.StringVar(&parent, "parent", "", "Parent item ref")
	f.BoolVar(&noParent, "no-parent", false, "Detach from the parent")
	f.StringVar(&agentNotes, "agent-notes", "", "New orchestrator notes")
	return cmd
}

// camel maps a kebab-case flag to its camelCase JSON key.
func camel(flag string) string {
	parts := strings.Split(flag, "-")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}
