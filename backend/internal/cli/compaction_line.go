package cli

import (
	"fmt"
	"io"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// writeCompactionLine prints a session's auto-compaction readout for session
// get and spawn: the measured baseline, the window Tallpine passes and where
// the agent compacts, for example
// "compaction: baseline 66k, window 166k, trigger 133k". Nothing without one.
func writeCompactionLine(out io.Writer, view *domain.AutoCompactView) error {
	if view == nil {
		return nil
	}
	_, err := fmt.Fprintf(out, "compaction: %s\n", formatCompaction(*view))
	return err
}

func formatCompaction(view domain.AutoCompactView) string {
	baseline := "not measured"
	if view.Baseline > 0 {
		baseline = formatTokensK(view.Baseline)
	}
	if view.Window <= 0 {
		return fmt.Sprintf("baseline %s, window agent default, trigger agent default", baseline)
	}
	return fmt.Sprintf("baseline %s, window %s, trigger %s", baseline, formatTokensK(int64(view.Window)), formatTokensK(int64(view.Trigger)))
}

// formatTokensK renders a token count in thousands: 166000 is "166k".
func formatTokensK(tokens int64) string {
	if tokens%1000 == 0 {
		return fmt.Sprintf("%dk", tokens/1000)
	}
	return fmt.Sprintf("%.1fk", float64(tokens)/1000)
}
