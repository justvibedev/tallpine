package project

import (
	"context"
	"strings"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// isDubiousOwnershipError reports whether git refused a repository because it
// is owned by a different user (git's "detected dubious ownership" safety
// check, common on Windows when the repo was created elevated).
func isDubiousOwnershipError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "dubious ownership")
}

// notAGitRepoError builds the NOT_A_GIT_REPO error. When git's own probe error
// says the repository was rejected for ownership, it names that cause and the
// safe.directory fix instead of the misleading "not a repository" message.
func notAGitRepoError(path string, probeErr error) error {
	if isDubiousOwnershipError(probeErr) {
		return apierr.Invalid("NOT_A_GIT_REPO", "Git refused this repository because it is owned by a different user (dubious ownership).", map[string]any{
			"path":         path,
			"suggestedFix": "Run `git config --global --add safe.directory \"" + path + "\"`, or take ownership of the folder, then try again.",
			"gitError":     probeErr.Error(),
		})
	}
	return apierr.Invalid("NOT_A_GIT_REPO", "Tallpine needs a Git repository with an initial commit before it can create agent workspaces.", nil)
}

// probeGitRepo returns whether path is the top level of a git repository, plus
// git's error when the probe itself failed, so callers can log and classify it.
func probeGitRepo(path string) (bool, error) {
	out, err := ports.DefaultGit().TopLevel(context.Background(), path)
	if err != nil {
		return false, err
	}
	top := normalizeGitReportedPath(path, out)
	return samePath(top, comparablePath(path)), nil
}
