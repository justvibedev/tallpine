package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// provisionWorkspace applies the project's per-workspace setup after the
// worktree exists: symlink shared files from the project repo, then run any
// post-create commands. Either failing aborts the spawn so a half-provisioned
// workspace never launches an agent.
func (m *Manager) provisionWorkspace(ctx context.Context, project domain.ProjectRecord, workspacePath string) error {
	if err := applySymlinks(project.Path, workspacePath, project.Config.Symlinks); err != nil {
		return err
	}
	return runPostCreate(ctx, workspacePath, project.Path, project.Config.PostCreate, project.Config.Env)
}

// applySymlinks links each repo-relative path into the workspace. A source that
// does not exist is skipped (symlinks are a convenience for optional files like
// .env); a real link failure aborts. Paths must be repo-relative with no
// parent traversal (no leading "/", no ".." segment) — a bad path is refused
// up front so a project config cannot escape the project or workspace tree.
func applySymlinks(projectPath, workspacePath string, symlinks []string) error {
	for _, rel := range symlinks {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		clean, err := safeRelPath(rel)
		if err != nil {
			return fmt.Errorf("symlink %q: %w", rel, err)
		}
		source := filepath.Join(projectPath, clean)
		if _, err := os.Stat(source); err != nil {
			continue
		}
		target := filepath.Join(workspacePath, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return fmt.Errorf("symlink %q: %w", rel, err)
		}
		if _, err := os.Lstat(target); err == nil {
			continue
		}
		if err := os.Symlink(source, target); err != nil {
			return fmt.Errorf("symlink %q: %w", rel, err)
		}
	}
	return nil
}

// safeRelPath confines rel to a repo-relative path: no absolute paths and no
// ".." segments (before or after Clean). The cleaned form is returned so
// callers join it against project/workspace roots safely.
func safeRelPath(rel string) (string, error) {
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return "", fmt.Errorf("path must be repo-relative")
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == "." || clean == "" {
		return "", fmt.Errorf("path must be repo-relative")
	}
	for _, seg := range strings.Split(filepath.ToSlash(clean), "/") {
		if seg == ".." {
			return "", fmt.Errorf("path must be repo-relative")
		}
	}
	return clean, nil
}

// scriptStepTimeoutUnit is the unit of a Setup or Cleanup step's time limit; tests
// shorten it.
var scriptStepTimeoutUnit = time.Minute

// scriptStepTimeout is the command a Setup or Cleanup step runs and its time
// limit: the one its first line sets (domain.ScriptStepMinutes), else
// fallbackMinutes (0 for none).
func scriptStepTimeout(step string, fallbackMinutes int) (string, time.Duration) {
	command, minutes := domain.ScriptStepMinutes(step)
	if minutes == 0 {
		minutes = fallbackMinutes
	}
	return command, time.Duration(minutes) * scriptStepTimeoutUnit
}

// runPostCreate runs each post-create command in the workspace via the platform
// shell (sh on Unix, cmd on Windows). Each step has its own deadline: the time
// limit its first line sets (domain.ParseTimedScriptCommand; the marker line
// itself never reaches the shell), else domain.DefaultScriptStepTimeoutMinutes.
// A non-zero exit aborts the spawn with a SetupStepError carrying bounded,
// redacted output.
func runPostCreate(ctx context.Context, workspacePath, sourcePath string, commands []string, projectEnv map[string]string) error {
	for index, step := range commands {
		command, timeout := scriptStepTimeout(step, domain.DefaultScriptStepTimeoutMinutes)
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		stepCtx, cancel := context.WithTimeout(ctx, timeout)
		out := &setupOutput{}
		env := agentlaunch.MergeEnv(projectEnv, map[string]string{
			"TALLPINE_SOURCE_TREE_PATH": sourcePath,
			"TALLPINE_WORKTREE_PATH":    workspacePath,
		})
		err := runWorkspaceStep(stepCtx, workspacePath, command, env, out)
		timedOut := ctx.Err() == nil && errors.Is(stepCtx.Err(), context.DeadlineExceeded)
		cancel()
		if err != nil {
			message := agentlaunch.RedactValues(strings.TrimSpace(out.String()), projectEnv)
			return &SetupStepError{Step: index + 1, Err: err, TimedOut: timedOut, Timeout: timeout, Output: message}
		}
	}
	return nil
}

// stepMinutes says a step's time limit in minutes ("1 minute", "30 minutes").
func stepMinutes(timeout time.Duration) string {
	if minutes := int(timeout / scriptStepTimeoutUnit); minutes != 1 {
		return strconv.Itoa(minutes) + " minutes"
	}
	return "1 minute"
}

// SetupBudget is how long a start of the project may spend in its Setup
// steps: the sum of their time limits.
func SetupBudget(project domain.ProjectRecord) time.Duration {
	return postCreateBudget(0, project.Config.PostCreate)
}

// postCreateBudget is how much longer than base a start that runs these Setup
// steps may take: the sum of their time limits, so a long Setup is never cut
// short by the start's own deadline.
func postCreateBudget(base time.Duration, commands []string) time.Duration {
	for _, step := range commands {
		command, timeout := scriptStepTimeout(step, domain.DefaultScriptStepTimeoutMinutes)
		if strings.TrimSpace(command) != "" {
			base += timeout
		}
	}
	return base
}

// SetupStepError is a project Setup command that failed. Its message is the
// one the user reads, so it names the step, the cause and where to fix it.
// Timeout is the step's time limit, which a timed-out step names.
type SetupStepError struct {
	Step     int
	Err      error
	TimedOut bool
	Timeout  time.Duration
	Output   string
}

func (e *SetupStepError) Error() string {
	cause := fmt.Sprintf("failed at step %d (%v)", e.Step, e.Err)
	if e.TimedOut {
		cause = fmt.Sprintf("stopped at step %d after %s", e.Step, stepMinutes(e.Timeout))
	}
	message := "The project's Setup script " + cause + ", so the agent did not start. Fix the script in Project settings > Scripts, then try again."
	if e.Output != "" {
		message += " Output: " + e.Output
	}
	return message
}

func (e *SetupStepError) Unwrap() error { return e.Err }

// setupOutput retains the last 8 KiB so a noisy installer cannot grow daemon memory.
type setupOutput struct{ tail []byte }

func (o *setupOutput) Write(p []byte) (int, error) {
	n := len(p)
	o.tail = append(o.tail, p...)
	if len(o.tail) > 8192 {
		o.tail = append([]byte(nil), o.tail[len(o.tail)-8192:]...)
	}
	return n, nil
}

func (o *setupOutput) String() string { return string(o.tail) }
