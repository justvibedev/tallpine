package cli

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
)

func newSessionLandCommand(ctx *commandContext) *cobra.Command {
	var into string
	var dryRun, killWorker, asJSON bool
	cmd := &cobra.Command{
		Use:   "land <session>",
		Short: "Squash a worker's approved branch (no PR) onto the target branch",
		Long: `Land a worker branch that has no PR (orchestrator or owner only; workers are refused).

1. Refuses a session that tracks a PR (use tallpine pr land).
2. Requires the newest native review of the branch to approve its current head.
3. Squashes the branch into one commit on the target: --into, else the project's
   default branch. The message carries Tallpine-Session and Tallpine-Head
   trailers; landing the same head again is a no-op.
4. Refuses a conflict (listing the files) and a checkout of the target with
   staged or modified tracked files. The target moves only by compare-and-swap
   and a clean checkout of it is fast-forwarded.
5. With a hosted remote, pushes the commit to origin without force (a moved
   remote rejects it and nothing moves), then fast-forwards the local target.

--dry-run prints the decision and moves nothing. --kill-worker ends the worker
after a successful land.`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			var res controllers.SessionLandResponse
			req := controllers.SessionLandRequest{Into: strings.TrimSpace(into), DryRun: dryRun}
			if err := ctx.postJSON(cmd.Context(), "sessions/"+url.PathEscape(id)+"/land", req, &res); err != nil {
				return err
			}
			if asJSON {
				if err := writeJSON(cmd.OutOrStdout(), res); err != nil {
					return err
				}
			} else if err := writeLandResult(cmd, id, res); err != nil {
				return err
			}
			if killWorker && !dryRun {
				return ctx.killSession(cmd.Context(), cmd, id, sessionOptions{})
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&into, "into", "", "target branch (default: the project's default branch)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the decision without moving anything")
	cmd.Flags().BoolVar(&killWorker, "kill-worker", false, "terminate the worker session after a successful land")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}

func writeLandResult(cmd *cobra.Command, id string, res controllers.SessionLandResponse) error {
	out := cmd.OutOrStdout()
	var err error
	switch {
	case res.DryRun:
		_, err = fmt.Fprintf(out, "dry-run: would squash %s onto %s at %s\n", id, res.Target, shortSHA(res.OldSha))
	case res.AlreadyLanded:
		_, err = fmt.Fprintf(out, "already landed: %s is %s on %s\n", id, shortSHA(res.NewSha), res.Target)
	default:
		_, err = fmt.Fprintf(out, "landed %s on %s: %s -> %s\n", id, res.Target, shortSHA(res.OldSha), shortSHA(res.NewSha))
	}
	if err != nil {
		return err
	}
	if res.Pushed {
		if _, err := fmt.Fprintf(out, "  pushed to origin/%s\n", res.Target); err != nil {
			return err
		}
	}
	for _, wt := range res.UpdatedCheckouts {
		if _, err := fmt.Fprintf(out, "  fast-forwarded checkout %s\n", wt); err != nil {
			return err
		}
	}
	for _, n := range res.Notes {
		if _, err := fmt.Fprintf(out, "  note: %s\n", n); err != nil {
			return err
		}
	}
	return nil
}
