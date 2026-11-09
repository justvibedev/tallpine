package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

// gitFatalExit is git's own status for a fatal error.
const gitFatalExit = 128

func newGitExecCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "git-exec <shim-dir> -- <git arguments>",
		Short: "run git for a session's git shim, refusing writes to the owner's remotes and shared config",
		Long: "The session's `git` shim runs this for commands naming `remote` or `config`. Linked worktrees share " +
			"their remotes and .git/config with the owner's checkout, so remote changes and default or --local " +
			"`git config` writes in the session's project are refused; everything else goes to the real git unchanged. " +
			"See docs/agent-policy.md.",
		Hidden:             true,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		SilenceErrors:      true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("usage: git-exec <shim-dir> -- <git arguments>")
			}
			return ctx.gitExec(cmd.Context(), args[0], trimDashDash(args[1:]), cmd.OutOrStdout(), cmd.ErrOrStderr(), cmd.InOrStdin())
		},
	}
}

func (c *commandContext) gitExec(ctx context.Context, shimDir string, args []string, stdout, stderr io.Writer, stdin io.Reader) error {
	gitPath, err := findOnPathOutside("git", os.Getenv("PATH"), shimDir)
	if err != nil {
		return err
	}
	if reason := gitRefusal(ctx, gitPath, args); reason != "" {
		_, _ = fmt.Fprintf(stderr, "tallpine git: %s\n", reason)
		return exitCodeError{code: gitFatalExit, silent: true}
	}
	cmd := tallpineprocess.CommandContext(ctx, gitPath, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		var exit interface{ ExitCode() int }
		if errors.As(err, &exit) {
			return exitCodeError{code: exit.ExitCode(), silent: true}
		}
		return err
	}
	return nil
}

// gitRefusal is why args must not run, empty when they may. Anything it cannot
// classify, and any repository outside the session's project, goes to git.
func gitRefusal(ctx context.Context, gitPath string, args []string) string {
	return gitRefusalIn(ctx, gitPath, "", args)
}

// gitRefusalIn is gitRefusal for a command that will run in dir, empty for the
// current directory.
func gitRefusalIn(ctx context.Context, gitPath, dir string, args []string) string {
	root := strings.TrimSpace(os.Getenv(agentlaunch.EnvGitGuardRoot))
	if root == "" || os.Getenv(agentlaunch.EnvAllowSharedGitConfig) == "1" {
		return ""
	}
	globals, command := splitGitGlobals(args)
	write := classifyGitWrite(command)
	if write.kind == gitWriteNone || !gitTargetsRoot(ctx, gitPath, dir, globals, root) {
		return ""
	}
	override := "If the owner explicitly asked for this change, rerun with " + agentlaunch.EnvAllowSharedGitConfig + "=1."
	switch write.kind {
	case gitWriteRemote:
		return "refused `git remote " + write.detail + "`: linked worktrees share remotes with the owner's checkout. " + override
	default:
		return "refused a `git config` write to the config shared with the owner's checkout; use `git config --worktree`. " + override
	}
}

// gitTargetsRoot reports whether the command's repository is root's, or one
// nested in it (a workspace project's child repos), by asking git so that -C,
// --git-dir and GIT_DIR all count. Outside a repository the answer is no and git
// reports its own error.
func gitTargetsRoot(ctx context.Context, gitPath, dir string, globals []string, root string) bool {
	args := append(append([]string{}, globals...), "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd := tallpineprocess.CommandContext(ctx, gitPath, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	common := cleanDir(strings.TrimSpace(string(out)))
	return common != "" && strings.HasPrefix(common+string(filepath.Separator), cleanDir(root)+string(filepath.Separator))
}
