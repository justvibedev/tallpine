package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

type sessionUsageResponse struct {
	Models []apitypes.ModelUsagePayload `json:"models"`
}

// newSessionUsageCommand prints a session's cumulative token usage by model,
// for reading a compaction A/B test from the app instead of transcripts.
func newSessionUsageCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "usage [id]",
		Short: "Print a session's cumulative token usage by model (defaults to the current session)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(brand.Getenv("SESSION_ID"))
			if len(args) == 1 {
				id = strings.TrimSpace(args[0])
			}
			if id == "" {
				return usageError{errors.New("session id is required outside a Tallpine session (TALLPINE_SESSION_ID is not set)")}
			}
			var response sessionUsageResponse
			if err := ctx.getJSON(cmd.Context(), "sessions/"+url.PathEscape(id)+"/usage", &response); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				if response.Models == nil {
					response.Models = []apitypes.ModelUsagePayload{}
				}
				encoder := json.NewEncoder(out)
				encoder.SetIndent("", "  ")
				return encoder.Encode(response)
			}
			if len(response.Models) == 0 {
				_, err := fmt.Fprintln(out, "no token usage recorded")
				return err
			}
			table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(table, "MODEL\tINPUT\tCACHE-WRITE\tCACHE-READ\tOUTPUT\tCREDITS\tTURNS")
			for _, m := range response.Models {
				_, _ = fmt.Fprintf(table, "%s\t%d\t%d\t%d\t%d\t%.2f\t%d\n", m.Model, m.InputTokens, m.CacheCreationTokens, m.CacheReadTokens, m.OutputTokens, m.Credits, m.Turns)
			}
			return table.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print machine-readable JSON")
	return cmd
}
