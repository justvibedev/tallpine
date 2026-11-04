package cli

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	mergessvc "tallpine.eu/tallpine/backend/internal/service/merges"
)

func newMergesCommand(ctx *commandContext) *cobra.Command {
	var project string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "merges",
		Short: "Show recently merged and in-flight PRs across lanes, with same-file overlaps",
		Long: "Prints what merged in the last 24h, what is in flight per lane, and which in-flight PRs touch the same files " +
			"(with merge-order advice). Run it before `gh pr merge` so same-area PRs from other lanes merge one at a time. " +
			"Changed files come from the provider and are capped, so a huge PR may show only part of its paths.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := ctx.resolveSpawnProject(cmd.Context(), project)
			if err != nil {
				return err
			}
			var d mergessvc.Digest
			if err := ctx.getJSON(cmd.Context(), "projects/"+url.PathEscape(resolved.ID)+"/merges", &d); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), d)
			}
			return writeMergeDigest(cmd.OutOrStdout(), d)
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	return cmd
}

func writeMergeDigest(out io.Writer, d mergessvc.Digest) error {
	var b strings.Builder
	b.WriteString("Recently merged (24h):\n")
	if len(d.RecentlyMerged) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, e := range d.RecentlyMerged {
		fmt.Fprintf(&b, "  #%d %s%s - %s\n", e.Number, laneLabel(e.Lane), e.Title, pathSummary(e.Paths))
	}
	b.WriteString("In flight:\n")
	if len(d.InFlight) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, e := range d.InFlight {
		fmt.Fprintf(&b, "  #%d %s%s [%s] - %s\n", e.Number, laneLabel(e.Lane), e.Title, e.State, pathSummary(e.Paths))
	}
	b.WriteString("Overlaps:\n")
	if len(d.Overlaps) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, o := range d.Overlaps {
		mark := ""
		if o.CrossLane {
			mark = " [cross-lane]"
		}
		fmt.Fprintf(&b, "  ! %s%s\n", o.Advice, mark)
	}
	_, err := io.WriteString(out, b.String())
	return err
}

func laneLabel(lane string) string {
	if lane == "" {
		return ""
	}
	return "[" + lane + "] "
}

func pathSummary(paths []string) string {
	switch {
	case len(paths) == 0:
		return "files unknown"
	case len(paths) <= 3:
		return strings.Join(paths, ", ")
	default:
		return fmt.Sprintf("%s, +%d more", strings.Join(paths[:3], ", "), len(paths)-3)
	}
}
