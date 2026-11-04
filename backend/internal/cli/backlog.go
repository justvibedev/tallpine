package cli

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

type backlogItemEnvelopeDTO struct {
	Item apitypes.BacklogItemResponse `json:"item"`
}

type backlogListDTO struct {
	Items []apitypes.BacklogItemResponse `json:"items"`
}

type backlogNextDTO struct {
	Item *apitypes.BacklogItemResponse `json:"item"`
}

func newBacklogCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backlog",
		Short: "Keep a per-project backlog of deferred work",
		Long:  "File, triage and order deferred work for a project. Items start in the inbox when an agent files them; accepted items are open and run in drag order.",
	}
	cmd.AddCommand(
		newBacklogAddCommand(ctx), newBacklogListCommand(ctx), newBacklogShowCommand(ctx),
		newBacklogEditCommand(ctx), newBacklogSetCommand(ctx), newBacklogLinkCommand(ctx, false), newBacklogLinkCommand(ctx, true),
		newBacklogStateCommand(ctx, "done", "Mark an item done", "done"),
		newBacklogStateCommand(ctx, "drop", "Drop an item (kept in the archive)", "dropped"),
		newBacklogStateCommand(ctx, "accept", "Accept an inbox item into the open list", "open"),
		newBacklogStateCommand(ctx, "reopen", "Reopen a done or dropped item", "open"),
		newBacklogDeleteCommand(ctx), newBacklogPurgeCommand(ctx),
		newBacklogHoldCommand(ctx), newBacklogUnholdCommand(ctx), newBacklogMoveCommand(ctx), newBacklogNextCommand(ctx),
		newBacklogRunCommand(ctx),
	)
	return cmd
}

// backlogPath resolves the project and returns the backlog route prefix.
func (c *commandContext) backlogPath(ctx context.Context, project string) (string, error) {
	resolved, err := c.resolveSpawnProject(ctx, project)
	if err != nil {
		return "", err
	}
	return "projects/" + url.PathEscape(resolved.ID) + "/backlog", nil
}

func backlogItemPath(base, ref string) string {
	return base + "/" + url.PathEscape(strings.TrimSpace(ref))
}

func addBacklogProjectFlag(cmd *cobra.Command, project *string, jsonOutput *bool) {
	cmd.Flags().StringVar(project, "project", "", "Project id (default: TALLPINE_PROJECT_ID, current session, or current registered repo)")
	cmd.Flags().BoolVar(jsonOutput, "json", false, "Print JSON")
}

// parseBacklogLinkSpec splits "kind:value" (blocked-by is accepted for blocked_by).
func parseBacklogLinkSpec(spec string) (kind, ref string, err error) {
	i := strings.Index(spec, ":")
	if i <= 0 || i == len(spec)-1 {
		return "", "", usageError{errors.New("link must look like kind:value (session, pr, issue, artifact, memory, item, blocked-by)")}
	}
	kind = strings.ReplaceAll(strings.ToLower(spec[:i]), "-", "_")
	return kind, spec[i+1:], nil
}
