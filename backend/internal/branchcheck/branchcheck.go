// Package branchcheck notices when a project's default branch, the one every
// session worktree is cut from, has been left behind by another branch of the
// same repository. It only reads; switching the default is the user's call.
package branchcheck

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/process"
)

// MinNewer is how much newer than the default's tip a branch must be to count
// as "much newer".
const MinNewer = 30 * 24 * time.Hour

// wellKnown are the branch names that are plausible defaults besides the one
// currently checked out in the project folder.
var wellKnown = []string{"main", "master", "trunk", "develop"}

// Finding is a branch that strictly contains the default branch and is much
// newer: sessions would start from far older code than the repository has.
type Finding struct {
	// Branch is the name to set as the project's default branch.
	Branch string
	// Ahead is how many commits Branch has that the default lacks.
	Ahead int
	// NewerDays is how much newer Branch's tip is than the default's tip.
	NewerDays int
	// CheckedOut is true when Branch is the project folder's current branch.
	CheckedOut bool
}

// Detect compares the ref sessions start from (baseRef, canonical, for example
// refs/remotes/origin/master or refs/heads/master) with the project folder's
// current branch and the usual default names, over the same remote. It returns
// nil unless one of them is a strict descendant of baseRef and at least
// minNewer newer. The checked-out branch wins; otherwise the one furthest ahead.
func Detect(ctx context.Context, repo, remote, baseRef string, minNewer time.Duration) (*Finding, error) {
	baseTip, err := tipTime(ctx, repo, baseRef)
	if err != nil {
		return nil, err
	}
	current, _ := run(ctx, repo, "symbolic-ref", "--short", "-q", "HEAD")
	baseName := strings.TrimPrefix(strings.TrimPrefix(baseRef, "refs/remotes/"+remote+"/"), "refs/heads/")
	var best *Finding
	seen := map[string]bool{baseName: true}
	for _, name := range append([]string{current}, wellKnown...) {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		ref := firstRef(ctx, repo, "refs/remotes/"+remote+"/"+name, "refs/heads/"+name)
		if ref == "" {
			continue
		}
		if _, err := run(ctx, repo, "merge-base", "--is-ancestor", baseRef, ref); err != nil {
			continue // diverged or behind
		}
		ahead, err := run(ctx, repo, "rev-list", "--count", baseRef+".."+ref)
		n, convErr := strconv.Atoi(ahead)
		if err != nil || convErr != nil || n == 0 {
			continue // same commit
		}
		tip, err := tipTime(ctx, repo, ref)
		if err != nil || tip.Sub(baseTip) < minNewer {
			continue
		}
		f := &Finding{Branch: name, Ahead: n, NewerDays: int(tip.Sub(baseTip).Hours() / 24), CheckedOut: name == current}
		// The checked-out branch is evaluated first and is never displaced.
		if best == nil || (!best.CheckedOut && f.Ahead > best.Ahead) {
			best = f
		}
	}
	return best, nil
}

func firstRef(ctx context.Context, repo string, refs ...string) string {
	for _, r := range refs {
		if _, err := run(ctx, repo, "rev-parse", "--verify", "-q", r+"^{commit}"); err == nil {
			return r
		}
	}
	return ""
}

func tipTime(ctx context.Context, repo, ref string) (time.Time, error) {
	out, err := run(ctx, repo, "log", "-1", "--format=%ct", ref)
	if err != nil {
		return time.Time{}, err
	}
	sec, err := strconv.ParseInt(out, 10, 64)
	return time.Unix(sec, 0), err
}

func run(ctx context.Context, repo string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := process.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...) //nolint:gosec // fixed git subcommands on the user's own checkout
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}
