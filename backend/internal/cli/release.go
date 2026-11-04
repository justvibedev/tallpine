package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
)

func newReleaseCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{Use: "release", Short: "Install local release builds"}
	cmd.AddCommand(&cobra.Command{
		Use:   "install <sha>",
		Short: "Have the daemon install a local release build (orchestrators)",
		Long: `Asks the daemon to install the build release-builds/<sha> (the 40-character
commit printed by scripts/release-local build). The daemon, not this shell,
runs scripts/release-local install, because an orchestrator's sandbox cannot
read the daemon token or database that the installer needs.

The daemon refuses unless the caller is an orchestrator session, the app
resolves to ~/.tallpine/release-builds/<sha>/frontend/out/**, and codesign
--deep --strict passes with the release Team ID. The install runs detached and
messages this session when it finishes; its log path is printed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sha := strings.TrimSpace(args[0])
			if sha == "" {
				return usageError{errors.New("usage: release install <sha>")}
			}
			var res controllers.ReleaseInstallResponse
			if err := ctx.postJSON(cmd.Context(), "release/install", controllers.ReleaseInstallRequest{SHA: sha}, &res); err != nil {
				return err
			}
			_, err := fmt.Fprintf(ctx.deps.Out, "install of %s started (pid %d); log %s\n", res.SHA, res.PID, res.Log)
			return err
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "request-build",
		Short: "Have the daemon request a local release build (agents)",
		Long: `Asks the daemon to run scripts/release-local request-build from this
project's own checkout. A fenced agent shell cannot write
~/.tallpine/release-builds, and must not: the daemon installs whatever is
there. The request runs detached; its log path is printed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var res controllers.ReleaseBuildResponse
			if err := ctx.postJSON(cmd.Context(), "release/request-build", struct{}{}, &res); err != nil {
				return err
			}
			_, err := fmt.Fprintf(ctx.deps.Out, "build request started (pid %d); log %s\n", res.PID, res.Log)
			return err
		},
	})
	return cmd
}
