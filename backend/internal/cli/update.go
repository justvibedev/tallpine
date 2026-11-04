package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	if s == "" {
		return "unknown"
	}
	return s
}

func newUpdateCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update and restart Tallpine to a CI-passed build (owner only)",
		Long: `Update this Mac's Tallpine to the newest origin/main commit with a
passing local CI row. Owner only: agent sessions (workers, orchestrators) are
refused by the daemon. The update runs as a detached job that quits and
relaunches the app and daemon, rolls back automatically if the new daemon is
not healthy, and records its result (see 'tallpine update status').`,
	}
	cmd.AddCommand(newUpdateStatusCommand(ctx), newUpdateStartCommand(ctx))
	return cmd
}

func refuseInAgentSession() error {
	if strings.TrimSpace(brand.Getenv("SESSION_ID")) != "" {
		return errors.New("tallpine update is for the owner: agent sessions cannot update the app (ask the owner to run it from Settings or a terminal)")
	}
	return nil
}

func newUpdateStatusCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the installed commit, the newest CI-passed commit and the last update result",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := refuseInAgentSession(); err != nil {
				return err
			}
			var st apitypes.UpdateStatusResponse
			if err := ctx.getJSON(cmd.Context(), "update", &st); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Installed: %s\n", shortSHA(st.InstalledCommit))
			switch {
			case st.Running:
				_, _ = fmt.Fprintln(out, "An update is running.")
			case st.UpdateAvailable:
				built := "will be built"
				if st.BuildAvailable {
					built = "already built"
				}
				_, _ = fmt.Fprintf(out, "Update available: %s (CI passed, %s). Run: tallpine update start\n", shortSHA(st.Candidate), built)
			case st.Candidate == "":
				_, _ = fmt.Fprintln(out, "No origin/main commit with a passing CI row was found.")
			default:
				_, _ = fmt.Fprintln(out, "Up to date.")
			}
			if st.LatestCommit != "" && !st.LatestCIPassed {
				_, _ = fmt.Fprintf(out, "origin/main head %s has no passing CI row yet.\n", shortSHA(st.LatestCommit))
			}
			if l := st.Last; l != nil {
				_, _ = fmt.Fprintf(out, "Last update: %s %s %s\n", l.State, shortSHA(l.Commit), l.Message)
			}
			return nil
		},
	}
}

func newUpdateStartCommand(ctx *commandContext) *cobra.Command {
	var allowDowngrade bool
	cmd := &cobra.Command{
		Use:   "start [commit]",
		Short: "Start the detached update job for a CI-passed commit (default: the newest)",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseInAgentSession(); err != nil {
				return err
			}
			var commit string
			if len(args) == 1 {
				commit = args[0]
			} else {
				var st apitypes.UpdateStatusResponse
				if err := ctx.getJSON(cmd.Context(), "update", &st); err != nil {
					return err
				}
				if !st.UpdateAvailable {
					return errors.New("no update available: run 'tallpine update status'")
				}
				commit = st.Candidate
			}
			var res apitypes.UpdateResult
			if err := ctx.postJSON(cmd.Context(), "update", map[string]any{"commit": commit, "allowDowngrade": allowDowngrade}, &res); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Update to %s started. The app and daemon will restart; watch 'tallpine update status'.\n", shortSHA(res.Commit))
			return err
		},
	}
	cmd.Flags().BoolVar(&allowDowngrade, "allow-downgrade", false, "install a commit older than the installed one")
	return cmd
}
