package cli

import (
	"fmt"
	"io"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

// printKeptWorktrees lists the worktrees the run kept for a reason the owner
// can act on, with that reason and where the salvaged work is (it is part of
// the reason text), so stale worktrees do not pile up unnoticed.
func printKeptWorktrees(w io.Writer, r apitypes.CleanupRunReport) error {
	var kept []apitypes.CleanupItem
	for _, cat := range r.Categories {
		for _, item := range cat.Items {
			if item.Attention && item.What == "worktree" {
				kept = append(kept, item)
			}
		}
	}
	if len(kept) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(w, "\nWorktrees kept by cleanup (%d):\n", len(kept)); err != nil {
		return err
	}
	for _, item := range kept {
		if _, err := fmt.Fprintf(w, "  %s  %s\n    %s\n", item.SessionID, item.Path, item.Reason); err != nil {
			return err
		}
	}
	return nil
}
