package cli

import "github.com/spf13/cobra"

func newBacklogShowCommand(ctx *commandContext) *cobra.Command {
	var project string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "show <ref>",
		Short: "Show one backlog item with its links",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := ctx.backlogPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			var response backlogItemEnvelopeDTO
			if err := ctx.getJSON(cmd.Context(), backlogItemPath(base, args[0]), &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			return writeBacklogItem(cmd.OutOrStdout(), response.Item)
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	return cmd
}

func newBacklogNextCommand(ctx *commandContext) *cobra.Command {
	var project, parent string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Show the topmost open, unheld, unblocked, non-parent item",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			base, err := ctx.backlogPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			path := base + "/next"
			if parent != "" {
				path += "?parent=" + queryEscape(parent)
			}
			var response backlogNextDTO
			if err := ctx.getJSON(cmd.Context(), path, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			if response.Item == nil {
				_, err := cmd.OutOrStdout().Write([]byte("nothing is ready: no open, unheld, unblocked item\n"))
				return err
			}
			return writeBacklogItem(cmd.OutOrStdout(), *response.Item)
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	cmd.Flags().StringVar(&parent, "parent", "", "Only children of this parent ref")
	return cmd
}
