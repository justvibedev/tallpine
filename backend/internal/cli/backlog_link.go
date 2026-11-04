package cli

import (
	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func newBacklogLinkCommand(ctx *commandContext, remove bool) *cobra.Command {
	var project string
	var jsonOutput bool
	use, short, verb, method := "link <ref> <kind:value>", "Link an item to a session, pr, issue, artifact, memory, item or blocked-by item", "linked", "POST"
	if remove {
		use, short, verb, method = "unlink <ref> <kind:value>", "Remove a link from an item", "unlinked", "DELETE"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, ref, err := parseBacklogLinkSpec(args[1])
			if err != nil {
				return err
			}
			base, err := ctx.backlogPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			var response backlogItemEnvelopeDTO
			if err := ctx.doJSON(cmd.Context(), method, backlogItemPath(base, args[0])+"/links", apitypes.BacklogLinkRequest{Kind: kind, Ref: ref}, &response); err != nil {
				return err
			}
			return printBacklogMutation(cmd, response, jsonOutput, verb)
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	return cmd
}
