package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func newOrchestratorEndCommand(ctx *commandContext) *cobra.Command {
	var project, lane, session string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "end",
		Short: "End a lane: remove its orchestrator and hand its workers to the default one",
		Long: "Fully removes a lane orchestrator: stops it and deletes its worktree, branch, artifacts and " +
			"session record. Its live workers move to the project's default orchestrator (started if none " +
			"runs) and are told. The backlog item is left as it is. Refused, changing nothing, when the " +
			"orchestrator's worktree has uncommitted or unpushed changes. Name it with --lane <ref> or --session <id>.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body := apitypes.EndLaneRequest{SessionID: domain.SessionID(strings.TrimSpace(session)), Lane: strings.TrimSpace(lane)}
			if (body.SessionID == "") == (body.Lane == "") {
				return usageError{errors.New("usage: pass exactly one of --lane <ref> or --session <id>")}
			}
			if string(body.SessionID) != "" && !sessionIDPattern.MatchString(string(body.SessionID)) {
				return usageError{errors.New("usage: --session is not a valid session id")}
			}
			if body.Lane != "" {
				resolved, err := ctx.resolveSpawnProject(cmd.Context(), project)
				if err != nil {
					return err
				}
				body.ProjectID = domain.ProjectID(resolved.ID)
			}
			var res apitypes.EndLaneResponse
			// No client timeout: stopping the orchestrator and removing its worktree can be slow.
			if err := ctx.postSetupJSON(cmd.Context(), "orchestrators/end", body, &res); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Removed %s. %d live worker(s) now report to %s.\n", res.Removed, len(res.Workers), res.Orchestrator)
			if err == nil && len(res.NotifyFailed) > 0 {
				_, err = fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not notify: %s\n", joinSessionIDs(res.NotifyFailed))
			}
			return err
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	cmd.Flags().StringVar(&lane, "lane", "", "Top-level backlog item ref (e.g. B12) of the lane to end")
	cmd.Flags().StringVar(&session, "session", "", "Lane orchestrator session id to end")
	return cmd
}

func joinSessionIDs(ids []domain.SessionID) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = string(id)
	}
	return strings.Join(parts, ", ")
}
