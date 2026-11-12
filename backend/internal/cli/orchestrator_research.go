package cli

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
)

func newOrchestratorResearchCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "research",
		Short: "Raise this orchestrator's compaction window while it examines a large codebase itself",
		Long: "Research mode (Claude Code orchestrators only) raises the compaction window, 250k by default, so material you read " +
			"yourself is not compacted away mid-analysis. Claude Code applies the window only at launch, so switching it on " +
			"restarts your agent once it is idle (your conversation is kept) and sends you a message to continue the reading. Plain coordination keeps the early window: switch it on only when you " +
			"decide a task needs you to read broadly, and off when the analysis is done. It also ends on its own when you are " +
			"idle-compacted or handed off, or after an hour without a turn. The owner can turn the feature off in Settings " +
			"(Dynamic orchestrator context) or per project (`tallpine project set --orchestrator-research-window off`).",
	}
	cmd.AddCommand(newResearchSwitchCommand(ctx, "on", true), newResearchSwitchCommand(ctx, "off", false), newResearchStatusCommand(ctx))
	return cmd
}

func researchSessionID(session string) (string, error) {
	id := strings.TrimSpace(session)
	if id == "" {
		id = strings.TrimSpace(brand.Getenv("SESSION_ID"))
	}
	if id == "" || !sessionIDPattern.MatchString(id) {
		return "", usageError{errors.New("usage: run inside an orchestrator session (TALLPINE_SESSION_ID) or pass --session")}
	}
	return "orchestrators/" + url.PathEscape(id) + "/research", nil
}

func writeResearchStatus(cmd *cobra.Command, res controllers.OrchestratorResearchResponse, asJSON bool) error {
	if asJSON {
		return writeJSON(cmd.OutOrStdout(), res)
	}
	state := "off (coordination window)"
	if res.Active {
		state = "on since " + res.Since
	} else if res.RestartPending {
		state = "on since " + res.Since + ", waiting for the agent to restart when idle"
	}
	window := "the agent's own default"
	if res.WindowTokens > 0 {
		window = fmt.Sprintf("%dk tokens", res.WindowTokens/1000)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Research mode %s; compaction window %s\n", state, window)
	return err
}

func newResearchSwitchCommand(ctx *commandContext, use string, on bool) *cobra.Command {
	var session string
	var asJSON bool
	short := "Switch research mode off and return to the coordination window"
	if on {
		short = "Switch research mode on: raise the compaction window for broad code examination"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := researchSessionID(session)
			if err != nil {
				return err
			}
			var res controllers.OrchestratorResearchResponse
			if err := ctx.postJSON(cmd.Context(), path, controllers.OrchestratorResearchRequest{Enabled: &on}, &res); err != nil {
				return err
			}
			return writeResearchStatus(cmd, res, asJSON)
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "Orchestrator session id (default: $TALLPINE_SESSION_ID)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func newResearchStatusCommand(ctx *commandContext) *cobra.Command {
	var session string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether research mode is on and the compaction window in effect",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := researchSessionID(session)
			if err != nil {
				return err
			}
			var res controllers.OrchestratorResearchResponse
			if err := ctx.getJSON(cmd.Context(), path, &res); err != nil {
				return err
			}
			return writeResearchStatus(cmd, res, asJSON)
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "Orchestrator session id (default: $TALLPINE_SESSION_ID)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}
