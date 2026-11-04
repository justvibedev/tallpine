package cli

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/migratevo"
)

func newMigrateFromVoCommand() *cobra.Command {
	var dryRun, copyHistory, skipLive, rollback, stopOrphans bool
	cmd := &cobra.Command{
		Use:   "migrate-from-vo",
		Short: "Move the old ~/.vo install to the Tallpine home ",
		Long: "Renames the old state root to the new one, rewrites path columns in the database, " +
			"repairs git worktrees and copies Claude project dirs for live sessions. The old app, " +
			"daemon and tmux server must be stopped. The run is resumable: rerun it after a failure.\n\n" +
			"--dry-run lists every action and changes nothing.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			roots, err := migratevo.DefaultRoots()
			if err != nil {
				return err
			}
			env := &migratevo.Env{
				Roots:       roots,
				Run:         migratevo.ExecRunner,
				Processes:   migratevo.PSProcesses,
				AliveTmux:   migratevo.TmuxAlive,
				SelfPID:     os.Getpid(),
				StopOrphans: stopOrphans, OrphanSettle: 2 * time.Second,
				Out: cmd.OutOrStdout(),
			}
			if skipLive {
				env.Processes, env.AliveTmux = nil, nil
			}
			if rollback {
				return env.Rollback(cmd.Context())
			}
			if home, err := os.UserHomeDir(); err == nil {
				agentsDir := filepath.Join(home, "Library", "LaunchAgents")
				env.LaunchdHook = func(ctx context.Context, _ *migratevo.Plan) error {
					return env.BootoutLegacyLaunchd(ctx, agentsDir, os.Getuid())
				}
			}
			// Step 8 (start the daemon, resume sessions) is the installer's app launch.
			env.DaemonHook = func(context.Context, *migratevo.Plan) error { return nil }
			return env.Migrate(cmd.Context(), migratevo.Options{DryRun: dryRun, CopyHarnessHistory: copyHistory})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "List every action without changing anything")
	cmd.Flags().BoolVar(&copyHistory, "copy-harness-history", true, "Copy Claude project dirs for live sessions")
	cmd.Flags().BoolVar(&skipLive, "skip-live-checks", false, "Skip the running-process and tmux checks (rehearsals against a scratch HOME only)")
	cmd.Flags().BoolVar(&rollback, "rollback", false, "Move the new home back to the old one and repair worktrees (used by release-local rollback)")
	cmd.Flags().BoolVar(&stopOrphans, "stop-orphans", false, "SIGTERM leftover processes running out of the old data/tmp (orphaned test binaries) before checking; nothing else is signalled")
	_ = cmd.Flags().MarkHidden("skip-live-checks")
	return cmd
}
