package session

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// workspaceGitCommand builds the hardened command for callers that pipe one
// git process into another.
func workspaceGitCommand(ctx context.Context, root string, args ...string) *exec.Cmd {
	return ports.DefaultGit().Command(ctx, root, args...)
}

func gitWorkspaceOutput(ctx context.Context, root string, args ...string) (string, error) {
	return gitWorkspaceOutputEnv(ctx, root, nil, args...)
}

// gitWorkspaceOutputEnv is gitWorkspaceOutput with extra environment entries,
// such as a scratch GIT_INDEX_FILE.
func gitWorkspaceOutputEnv(ctx context.Context, root string, env []string, args ...string) (string, error) {
	out, err := ports.DefaultGit().OutputEnv(ctx, root, env, args...)
	return out, classifyGitPortError(ctx, err)
}

// gitWorkspaceOutputCapped drains all output while retaining only a bounded
// prefix, preventing large diffs from becoming an unbounded in-memory buffer.
func gitWorkspaceOutputCapped(ctx context.Context, root string, limit int, args ...string) (string, bool, error) {
	out, truncated, err := ports.DefaultGit().OutputCapped(ctx, root, limit, args...)
	return out, truncated, classifyGitPortError(ctx, err)
}

// classifyGitPortError maps a failed ports.Git call to the actionable error
// the session read surface returns. A deleted owning repository stays the
// ports sentinel so it maps to a clean 404 (PROJECT_FOLDER_MISSING).
func classifyGitPortError(ctx context.Context, err error) error {
	if err == nil || errors.Is(err, ports.ErrWorkspaceRepoUnavailable) {
		return err
	}
	var gitErr *ports.GitError
	if errors.As(err, &gitErr) {
		return classifyWorkspaceGitError(ctx, gitErr.Err, gitErr.Detail)
	}
	return classifyWorkspaceGitError(ctx, err, "")
}

// classifyWorkspaceGitError keeps repository-read failures actionable at the
// HTTP boundary. The old code returned raw exec.ExitError values, which the
// envelope rendered as an indistinguishable INTERNAL_ERROR. Context deadlines
// remain discoverable by the envelope's transient mapper, while lock/contention
// failures are explicitly retryable.
func classifyWorkspaceGitError(ctx context.Context, err error, detail string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	lower := strings.ToLower(detail)
	for _, marker := range []string{
		"index.lock",
		"unable to create",
		"could not lock",
		"resource temporarily unavailable",
		"another git process",
	} {
		if strings.Contains(lower, marker) {
			return apierr.Unavailable("WORKSPACE_GIT_BUSY", "Workspace Git state is temporarily unavailable")
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return apierr.Internal("WORKSPACE_GIT_READ_FAILED", "Unable to read workspace Git state")
}

// cappedWorkspaceOutput bounds the stderr kept from a piped git process.
type cappedWorkspaceOutput = ports.CappedBuffer
