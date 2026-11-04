package review

import (
	stdctx "context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/process"
)

// LocalTargetPrefix starts the synthetic review key of a session branch that
// has no PR. Runs for such a branch are stored under local:<branch> in the
// PR URL column; nothing else is stored.
const LocalTargetPrefix = "local:"

// ErrNoLocalCommits means the session branch has nothing beyond its base ref.
var ErrNoLocalCommits = errors.New("nothing to review: the branch has no commits beyond its base")

var errRefMissing = errors.New("ref not found")

// LocalTargetURL is the review key of a session branch.
func LocalTargetURL(branch string) string { return LocalTargetPrefix + branch }

// IsLocalTarget reports whether a review key names a local branch, not a PR.
func IsLocalTarget(url string) bool { return strings.HasPrefix(url, LocalTargetPrefix) }

// LocalTarget synthesizes the review target of a worker's branch: head is the
// branch tip, BaseSHA the tip of the session's diff base ref. It reports ok
// only for a branch that was never pushed and has commits beyond its base. A
// pushed branch (a remote-tracking ref exists) is a PR-flow branch and keeps
// the "no PR tracked" path. ErrNoLocalCommits is returned for a local branch
// with nothing to review. The caller has already checked the worker has no
// tracked PR.
func LocalTarget(ctx stdctx.Context, worker domain.SessionRecord) (domain.PullRequest, bool, error) {
	ws, branch := worker.Metadata.WorkspacePath, strings.TrimSpace(worker.Metadata.Branch)
	baseRef := strings.TrimSpace(worker.Metadata.DiffBaseRef)
	if baseRef == "" {
		baseRef = strings.TrimSpace(worker.Metadata.DiffBaseSHA)
	}
	if ws == "" || branch == "" || baseRef == "" {
		return domain.PullRequest{}, false, nil
	}
	head, err := gitRevParse(ctx, ws, "refs/heads/"+branch)
	if errors.Is(err, errRefMissing) {
		return domain.PullRequest{}, false, nil // no such local branch (detached or deleted)
	}
	if err != nil {
		return domain.PullRequest{}, false, err
	}
	pushed, err := hasRemoteTracking(ctx, ws, branch)
	if err != nil {
		return domain.PullRequest{}, false, err
	}
	if pushed {
		return domain.PullRequest{}, false, nil
	}
	base, err := gitRevParse(ctx, ws, baseRef)
	if err != nil && worker.Metadata.DiffBaseSHA != "" && worker.Metadata.DiffBaseSHA != baseRef {
		base, err = gitRevParse(ctx, ws, worker.Metadata.DiffBaseSHA)
	}
	if err != nil {
		return domain.PullRequest{}, false, fmt.Errorf("resolve base %q: %w", baseRef, err)
	}
	out, err := process.CommandContext(ctx, "git", "-C", ws, "rev-list", "--count", base+".."+head).Output()
	if err != nil {
		return domain.PullRequest{}, false, fmt.Errorf("count commits on %s: %w", branch, err)
	}
	if strings.TrimSpace(string(out)) == "0" {
		return domain.PullRequest{}, false, fmt.Errorf("%w (%s vs %s)", ErrNoLocalCommits, branch, baseRef)
	}
	title := strings.TrimSpace(worker.DisplayName)
	if title == "" {
		title = branch
	}
	return domain.PullRequest{
		URL:          LocalTargetURL(branch),
		SessionID:    worker.ID,
		SourceBranch: branch,
		HeadSHA:      head,
		BaseSHA:      base,
		Title:        title,
	}, true, nil
}

// WithLocalTarget returns prs, or the worker's synthesized local target when
// the worker tracks no PR at all and has one. A branch with nothing to review
// leaves prs unchanged.
func WithLocalTarget(ctx stdctx.Context, worker domain.SessionRecord, prs []domain.PullRequest) ([]domain.PullRequest, error) {
	if len(prs) != 0 {
		return prs, nil
	}
	pr, ok, err := LocalTarget(ctx, worker)
	switch {
	case errors.Is(err, ErrNoLocalCommits):
		return prs, nil
	case err != nil:
		// Callers only enrich a display or a notice with this; the trigger path
		// calls LocalTarget itself and keeps the hard error.
		slog.Warn("review: local target unavailable", "session", worker.ID, "err", err)
		return prs, nil //nolint:nilerr // degrade: the display works without the local target
	case !ok:
		return prs, nil
	}
	return []domain.PullRequest{pr}, nil
}

func gitRevParse(ctx stdctx.Context, ws, ref string) (string, error) {
	out, err := process.CommandContext(ctx, "git", "-C", ws, "rev-parse", "--verify", "--quiet", ref+"^{commit}").Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return "", fmt.Errorf("%w: %s", errRefMissing, ref)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func hasRemoteTracking(ctx stdctx.Context, ws, branch string) (bool, error) {
	out, err := process.CommandContext(ctx, "git", "-C", ws, "for-each-ref", "--format=%(refname)", "refs/remotes").Output()
	if err != nil {
		return false, err
	}
	for _, ref := range strings.Split(string(out), "\n") {
		rest, ok := strings.CutPrefix(ref, "refs/remotes/")
		if !ok {
			continue
		}
		if _, name, found := strings.Cut(rest, "/"); found && name == branch {
			return true, nil
		}
	}
	return false, nil
}
