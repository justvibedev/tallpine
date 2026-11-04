package cli

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/brand"
)

type sessionCommitResponse struct {
	Sha string `json:"sha"`
}

func newCommitCommand(ctx *commandContext) *cobra.Command {
	var message string
	cmd := &cobra.Command{
		Use:   "commit -m <message>",
		Short: "Commit all changes in this session's worktree through the daemon",
		Long: "For sandboxed agents whose own `git add`/`git commit` is denied. Asks the daemon (as this session) to stage " +
			"every change in the session's own worktree and commit it with the given message. Hooks are off and nothing is pushed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			session := strings.TrimSpace(brand.Getenv("SESSION_ID"))
			if session == "" {
				return usageError{errors.New("usage: TALLPINE_SESSION_ID is required (run this inside a Tallpine session)")}
			}
			if strings.TrimSpace(message) == "" {
				return usageError{errors.New("usage: tallpine commit -m <message>")}
			}
			var out sessionCommitResponse
			if err := ctx.postJSON(cmd.Context(), "sessions/"+url.PathEscape(session)+"/commit", map[string]string{"message": message}, &out); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), out.Sha)
			return err
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "Commit message")
	return cmd
}
