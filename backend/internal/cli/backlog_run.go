package cli

import (
	"context"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

type backlogRunEnvelopeDTO struct {
	Run apitypes.BacklogRunResponse `json:"run"`
}

type backlogRunListDTO struct {
	Runs []apitypes.BacklogRunResponse `json:"runs"`
}

func newBacklogRunCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run mode: plan and drive \"finish the backlog\"",
		Long: "Run mode keeps one durable run per project: the plan (lanes of items, a model each), every item's progress, and the end summary. " +
			"The orchestrator writes the plan and the progress; the Backlog page reads them. On by default; TALLPINE_BACKLOG_RUN=off on the daemon turns it off.",
	}
	cmd.AddCommand(
		newBacklogRunStartCommand(ctx), newBacklogRunPlanCommand(ctx), newBacklogRunStatusCommand(ctx),
		newBacklogRunControlCommand(ctx, "pause", "Pause a running run (for example at the usage limit)"),
		newBacklogRunControlCommand(ctx, "resume", "Resume a paused run"),
		newBacklogRunControlCommand(ctx, "stop", "Finish the work in flight and start nothing new"),
		newBacklogRunItemCommand(ctx), newBacklogRunFinishCommand(ctx),
	)
	return cmd
}

// backlogRunPath resolves the project and returns the runs route prefix.
func (c *commandContext) backlogRunPath(ctx context.Context, project string) (string, error) {
	base, err := c.backlogPath(ctx, project)
	if err != nil {
		return "", err
	}
	return base + "-runs", nil
}

func backlogRunRefPath(base, ref string) string {
	return base + "/" + url.PathEscape(strings.TrimSpace(ref))
}
