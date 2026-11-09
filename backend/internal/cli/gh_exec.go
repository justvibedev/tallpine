package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

func newGHExecCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "gh-exec -- <gh arguments>",
		Short: "run gh with a GitHub token fetched from the daemon for this call only",
		Long: "The session's `gh` shim runs this. It asks the daemon (as this session) for the project's GitHub token, " +
			"finds the real gh on PATH (skipping the shim) and runs it with GH_TOKEN set in that child process only, so " +
			"the token is never in the agent's environment. When the daemon refuses, gh runs without a token (the " +
			"session's gh config directory is empty, so it is simply not logged in) and the reason is printed on stderr. " +
			"See docs/agent-policy.md.",
		Hidden:             true,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		SilenceErrors:      true,
		RunE: func(cmd *cobra.Command, args []string) error {
			args = trimDashDash(args)
			return ctx.ghExec(cmd.Context(), args, cmd.OutOrStdout(), cmd.ErrOrStderr(), cmd.InOrStdin())
		},
	}
}

func trimDashDash(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		return args[1:]
	}
	return args
}

func (c *commandContext) ghExec(ctx context.Context, args []string, stdout, stderr io.Writer, stdin io.Reader) error {
	ghPath, err := findRealGH(os.Getenv("PATH"), os.Getenv(agentlaunch.EnvGHShimDir))
	if err != nil {
		return err
	}
	env := os.Environ()
	host := strings.TrimSpace(os.Getenv("GH_HOST"))
	if host == "" {
		host = "github.com"
	}
	cred, err := c.requestGitHubCredential(ctx, apitypes.GitHubCredentialRequest{Host: host})
	switch {
	case err == nil:
		env = append(env, "GH_TOKEN="+cred.Token)
	case refusalReason(err) != "":
		_, _ = fmt.Fprintf(stderr, "tallpine gh: %s\n", refusalReason(err))
	default:
		_, _ = fmt.Fprintf(stderr, "tallpine gh: could not get a GitHub token from the daemon: %v\n", err)
	}
	cmd := tallpineprocess.CommandContext(ctx, ghPath, args...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		var exit interface{ ExitCode() int }
		if errors.As(err, &exit) {
			return exitCodeError{code: exit.ExitCode()}
		}
		return err
	}
	return nil
}

// findRealGH returns the first gh on PATH that is not in shimDir.
func findRealGH(path, shimDir string) (string, error) {
	return findOnPathOutside("gh", path, shimDir)
}

// findOnPathOutside returns the first executable tool on PATH that is not in
// shimDir, so a shim never runs itself.
func findOnPathOutside(tool, path, shimDir string) (string, error) {
	name := tool
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	shim := cleanDir(shimDir)
	for _, dir := range filepath.SplitList(path) {
		if dir == "" || (shim != "" && cleanDir(dir) == shim) {
			continue
		}
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && (runtime.GOOS == "windows" || info.Mode()&0o111 != 0) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("tallpine %s: the %s CLI is not installed (not found on PATH outside the session shim)", tool, tool)
}

func cleanDir(dir string) string {
	if dir == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	return filepath.Clean(dir)
}

// exitCodeError carries the exit status of a child process out as this command's.
// A silent one has already said everything on stderr, so main prints nothing.
type exitCodeError struct {
	code   int
	silent bool
}

func (e exitCodeError) Error() string {
	if e.silent {
		return ""
	}
	return fmt.Sprintf("exit status %d", e.code)
}
