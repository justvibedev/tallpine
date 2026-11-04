package cli

import (
	"net/url"
	"strconv"

	"github.com/spf13/cobra"
)

func newBacklogListCommand(ctx *commandContext) *cobra.Command {
	var project, state, parent, query string
	var held, unheld, jsonOutput bool
	var limit int
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List backlog items in drag order",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if held && unheld {
				return usageError{errConflictingHeldFlags}
			}
			base, err := ctx.backlogPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			q := url.Values{}
			set := func(k, v string) {
				if v != "" {
					q.Set(k, v)
				}
			}
			if state == "" {
				state = "active" // inbox + open; --state all shows closed items too
			}
			set("state", state)
			set("parent", parent)
			set("q", query)
			if held {
				q.Set("held", "held")
			}
			if unheld {
				q.Set("held", "unheld")
			}
			if limit > 0 {
				q.Set("limit", strconv.Itoa(limit))
			}
			path := base
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			var response backlogListDTO
			if err := ctx.getJSON(cmd.Context(), path, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			return writeBacklogTable(cmd.OutOrStdout(), response.Items)
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	f := cmd.Flags()
	f.StringVar(&state, "state", "", "Only inbox, open, done or dropped items; default inbox+open, all includes closed ones")
	f.StringVar(&parent, "parent", "", "Only children of this parent ref")
	f.StringVar(&query, "q", "", "Title substring")
	f.BoolVar(&held, "held", false, "Only held items")
	f.BoolVar(&unheld, "unheld", false, "Only unheld items")
	f.IntVar(&limit, "limit", 0, "Maximum items (default 100)")
	return cmd
}
