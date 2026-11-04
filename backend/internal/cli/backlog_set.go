package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

type backlogFieldDTO struct {
	Value string `json:"value"`
}

func newBacklogSetCommand(ctx *commandContext) *cobra.Command {
	var project string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "set <ref> key=value...",
		Short: "Set custom fields on an item (key= clears one)",
		Args:  usageArgs(cobra.MinimumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			pairs := make([][2]string, 0, len(args)-1)
			for _, arg := range args[1:] {
				key, value, ok := strings.Cut(arg, "=")
				if !ok || key == "" {
					return usageError{errNotKeyValue(arg)}
				}
				pairs = append(pairs, [2]string{key, value})
			}
			base, err := ctx.backlogPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			var response backlogItemEnvelopeDTO
			for _, kv := range pairs {
				path := backlogItemPath(base, args[0]) + "/fields/" + queryEscape(kv[0])
				if err := ctx.putJSON(cmd.Context(), path, backlogFieldDTO{Value: kv[1]}, &response); err != nil {
					return err
				}
			}
			return printBacklogMutation(cmd, response, jsonOutput, "updated")
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	return cmd
}
