// Package gitcli runs the git command line for services that read or inspect
// repositories. It owns the hardening every such call needs: no pager, no
// optional locks, no hooks, no external diff, no prompts, and an environment
// scrubbed of the variables that would point git at a different repository.
package gitcli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

const stderrLimit = 64 * 1024

// Runner implements ports.Git over the git binary on PATH.
type Runner struct {
	// Timeout bounds each call when positive and the context has no earlier
	// deadline.
	Timeout time.Duration
}

var _ ports.Git = (*Runner)(nil)

// New returns a Runner with no extra timeout.
func New() *Runner { return &Runner{} }

// Env returns environ with the repository-redirecting variables removed and
// the non-interactive settings added.
func Env(environ []string) []string { return ports.GitEnv(environ) }

func (r *Runner) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	if r.Timeout > 0 {
		return context.WithTimeout(ctx, r.Timeout)
	}
	return ctx, func() {}
}

// Command implements ports.Git. A Runner Timeout does not apply to a command
// the caller drives itself.
func (r *Runner) Command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	global := make([]string, 0, 10+len(args))
	global = append(global, "--no-pager", "--no-optional-locks")
	global = append(global, ports.GitHardenedArgs()...)
	if dir != "" {
		global = append(global, "-C", dir)
	}
	cmd := tallpineprocess.CommandContext(ctx, "git", append(global, noExternalDiff(args)...)...)
	cmd.Env = Env(os.Environ())
	return cmd
}

// diffCommands render patches and so would run a repository's configured
// diff.external or textconv program. `-c diff.external=` cannot disable it (an
// empty value makes plain `git diff` fail), so the flags go on the command itself.
var diffCommands = map[string]bool{"diff": true, "log": true, "show": true, "format-patch": true, "whatchanged": true}

// noExternalDiff returns args with --no-ext-diff --no-textconv after a patch-producing
// subcommand. It leaves other commands alone.
func noExternalDiff(args []string) []string {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-c" || args[i] == "-C":
			i++
		case strings.HasPrefix(args[i], "-"):
		default:
			if !diffCommands[args[i]] {
				return args
			}
			out := make([]string, 0, len(args)+2)
			out = append(out, args[:i+1]...)
			out = append(out, "--no-ext-diff", "--no-textconv")
			return append(out, args[i+1:]...)
		}
	}
	return args
}

// Plain implements ports.Git.
func (r *Runner) Plain(ctx context.Context, dir string, args ...string) *exec.Cmd {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := tallpineprocess.CommandContext(ctx, "git", args...)
	cmd.Env = Env(os.Environ())
	return cmd
}

// Run implements ports.Git.
func (r *Runner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	return r.output(ctx, dir, r.Plain, nil, args)
}

// Isolated implements ports.Git.
func (r *Runner) Isolated(ctx context.Context, dir string, env []string, args ...string) *exec.Cmd {
	cmd := tallpineprocess.CommandContext(ctx, "git", args...) //nolint:gosec // argv is the caller's fixed list
	cmd.Dir = dir
	cmd.Env = env
	return cmd
}

// Output implements ports.Git.
func (r *Runner) Output(ctx context.Context, dir string, args ...string) (string, error) {
	return r.OutputEnv(ctx, dir, nil, args...)
}

// OutputEnv implements ports.Git.
func (r *Runner) OutputEnv(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	return r.output(ctx, dir, r.Command, env, args)
}

func (r *Runner) output(ctx context.Context, dir string, build func(context.Context, string, ...string) *exec.Cmd, env, args []string) (string, error) {
	ctx, cancel := r.bound(ctx)
	defer cancel()
	cmd := build(ctx, dir, args...)
	cmd.Env = append(cmd.Env, env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if stdout := strings.TrimSpace(string(out)); stdout != "" {
			if detail != "" {
				detail += ": "
			}
			detail += stdout
		}
		// A worktree whose owning repository was deleted fails with an opaque
		// exit 128; report that one condition as its own sentinel.
		if RepoUnavailable(dir) {
			return "", fmt.Errorf("git -C %s %s: %w", dir, strings.Join(args, " "), ports.ErrWorkspaceRepoUnavailable)
		}
		return "", &ports.GitError{Args: args, Detail: detail, Err: err}
	}
	return string(out), nil
}

// OutputCapped implements ports.Git.
func (r *Runner) OutputCapped(ctx context.Context, dir string, limit int, args ...string) (string, bool, error) {
	ctx, cancel := r.bound(ctx)
	defer cancel()
	cmd := r.Command(ctx, dir, args...)
	stdout := &ports.CappedBuffer{Limit: limit}
	stderr := &ports.CappedBuffer{Limit: stderrLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if RepoUnavailable(dir) {
			return "", false, fmt.Errorf("workspace git command: %w", ports.ErrWorkspaceRepoUnavailable)
		}
		return "", false, &ports.GitError{Args: args, Detail: strings.TrimSpace(stderr.String()), Err: err}
	}
	return stdout.String(), stdout.Truncated(), nil
}

// TopLevel implements ports.Git.
func (r *Runner) TopLevel(ctx context.Context, dir string) (string, error) {
	out, err := r.Output(ctx, dir, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(out), err
}

// RemoteURL implements ports.Git.
func (r *Runner) RemoteURL(ctx context.Context, dir, remote string) (string, error) {
	out, err := r.Output(ctx, dir, "config", "--get", "remote."+remote+".url")
	return strings.TrimSpace(out), err
}

// CommonDir implements ports.Git.
func (r *Runner) CommonDir(ctx context.Context, dir string) (string, error) {
	out, err := r.Output(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	return strings.TrimSpace(out), err
}

// RepoUnavailable reports whether a worktree root can no longer reach its
// owning repository on disk. A worktree keeps a `.git` file whose `gitdir:`
// line names the repository's git directory; a plain checkout keeps a `.git`
// directory. Only the genuinely-gone case returns true.
func RepoUnavailable(root string) bool {
	gitPath := filepath.Join(root, ".git")
	if data, err := os.ReadFile(gitPath); err == nil {
		gitdir, ok := parseGitDirFile(string(data))
		if !ok {
			return false
		}
		if !filepath.IsAbs(gitdir) {
			gitdir = filepath.Join(root, gitdir)
		}
		info, err := os.Stat(gitdir) // #nosec G703 -- existence check of the gitdir a workspace .git pointer names; result is a bool only
		return err != nil || !info.IsDir()
	}
	info, err := os.Stat(gitPath)
	return err != nil || !info.IsDir()
}

func parseGitDirFile(content string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		const prefix = "gitdir:"
		if strings.HasPrefix(line, prefix) {
			gitdir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			if gitdir != "" {
				return gitdir, true
			}
		}
	}
	return "", false
}
