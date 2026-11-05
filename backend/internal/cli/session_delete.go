package cli

import (
	"context"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

type sessionDeleteOptions struct {
	project string
	yes     bool
	cleanup bool
}

func newSessionDeleteCommand(ctx *commandContext) *cobra.Command {
	var opts sessionDeleteOptions
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Permanently delete a terminated session",
		Long: "Permanently delete a terminated session's record and its Tallpine history (conversation, PR facts, attachments and artifacts). " +
			"The session must be terminated and its worktree cleaned up first (tallpine session cleanup, or --cleanup). " +
			"Git branches, pull requests and agent account usage totals are kept, and the session's id is never reused.",
		Args: oneSessionIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			return ctx.deleteSession(cmd.Context(), cmd, id, opts)
		},
	}
	addSessionProjectFlag(cmd.Flags(), &opts.project, "Project id to scope the lookup")
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&opts.cleanup, "cleanup", false, "Clean up a worktree still on disk first (never forced; a dirty or locked one refuses the delete)")
	return cmd
}

func (c *commandContext) deleteSession(ctx context.Context, cmd *cobra.Command, id string, opts sessionDeleteOptions) error {
	// Look the session up before asking, so an unknown id fails without a
	// prompt; with --yes the delete itself reports it.
	if opts.project != "" || !opts.yes {
		if _, err := c.fetchScopedSession(ctx, id, opts.project); err != nil {
			return err
		}
	}
	out := cmd.OutOrStdout()
	if !opts.yes {
		confirmed, err := confirmTyped(cmd, fmt.Sprintf("Permanently delete session %s and its history? Type yes to confirm: ", id))
		if err != nil {
			return err
		}
		if !confirmed {
			_, err := fmt.Fprintln(out, "aborted")
			return err
		}
	}
	params := url.Values{}
	if opts.cleanup {
		params.Set("cleanup", "true")
	}
	var res apitypes.DeleteSessionResponse
	if err := c.deleteJSON(ctx, apiPath("sessions/"+url.PathEscape(id), params), &res); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "session %s deleted\n", res.SessionID)
	return err
}
