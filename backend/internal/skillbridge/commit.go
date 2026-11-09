package skillbridge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/process"
)

// CommitMessage is the message of the commit that publishes created
// instruction files and skill links to the branch sessions start from.
const CommitMessage = "chore: make agent instructions and skills visible to sessions"

// CommitTarget is the ref new session worktrees are cut from, as the workspace
// adapter resolves it: refs/remotes/<remote>/<branch> when that tracking ref
// exists, otherwise refs/heads/<branch>.
type CommitTarget struct {
	Remote  string
	Branch  string
	BaseRef string
	// PushEnv authenticates the push as the project's GitHub account. It holds a
	// secret: pass it to git only and never log it.
	PushEnv []string
}

// NeedsPush reports whether sessions start from the remote-tracking ref, so a
// local commit alone does not reach them.
func (t CommitTarget) NeedsPush() bool { return strings.HasPrefix(t.BaseRef, "refs/remotes/") }

// CommitResult describes what CommitPaths did. The commit always exists when
// err is nil; it is reachable from the target branch, the remote, or
// SideBranch.
type CommitResult struct {
	SHA string
	// LocalUpdated is true when refs/heads/<branch> was fast-forwarded to SHA.
	LocalUpdated bool
	// Pushed is true when SHA was pushed to <remote>/<branch>.
	Pushed bool
	// PushError is the git error when the push was refused; the commit is kept.
	PushError string
	// SideBranch is the local branch created to keep SHA when neither the
	// local default branch nor the remote took it.
	SideBranch string
}

// Errors CommitPaths reports before it writes anything.
var (
	ErrNothingToCommit = errors.New("nothing to commit")
	ErrPathNotAllowed  = errors.New("path is not an uncommitted instruction file or skill")
	ErrPathExists      = errors.New("path already exists on the target branch")
)

const gitTimeout = 30 * time.Second

// CommitPaths commits exactly paths, as they are in the workspace checkout, on
// top of target.BaseRef, and publishes the commit to the branch new sessions
// start from. Only paths listed in allowed may be committed.
//
// It never touches the checkout's index, working tree or staged changes: the
// commit is built in a temporary index seeded from the target ref, so it works
// whether or not the default branch is checked out and whatever else is dirty.
// When sessions start from the remote, the commit is pushed without force and
// the local branch is fast-forwarded only after the push succeeded; a refused
// push keeps the commit on a side branch and reports the error. The local
// branch moves only when it sits exactly on the commit's parent (and, if it is
// checked out, only in this checkout, whose index then gains just these paths).
// The caller should refresh the remote-tracking ref first.
func CommitPaths(ctx context.Context, workspace string, target CommitTarget, paths, allowed []string, message string) (CommitResult, error) {
	clean, err := checkPaths(workspace, paths, allowed)
	if err != nil {
		return CommitResult{}, err
	}
	if target.Branch == "" || target.BaseRef == "" {
		return CommitResult{}, errors.New("default branch is not resolved")
	}
	parent, err := gitOut(ctx, workspace, nil, "rev-parse", "--verify", "-q", target.BaseRef+"^{commit}")
	if err != nil {
		return CommitResult{}, fmt.Errorf("default branch %s not found: %w", target.BaseRef, err)
	}
	for _, p := range clean {
		if _, err := gitOut(ctx, workspace, nil, "cat-file", "-e", parent+":"+p); err == nil {
			return CommitResult{}, fmt.Errorf("%w: %s", ErrPathExists, p)
		}
	}
	sha, err := commitTree(ctx, workspace, parent, clean, message)
	if err != nil {
		return CommitResult{}, err
	}
	res := CommitResult{SHA: sha}
	// When sessions start from the remote, the push decides: the local branch is
	// fast-forwarded only after the remote took the commit, so a refused push
	// never leaves local history ahead of (or diverged from) the remote.
	if target.NeedsPush() {
		if _, perr := gitOut(ctx, workspace, pushEnv(target.PushEnv), "push", "--", target.Remote, sha+":refs/heads/"+target.Branch); perr != nil {
			res.PushError = perr.Error()
		} else {
			res.Pushed = true
			// git updates the tracking ref on push when the fetch refspec maps it;
			// set it explicitly (compare-and-swap) so the next session sees it.
			_ = gitOK(ctx, workspace, "update-ref", target.BaseRef, sha, parent)
		}
	}
	if res.Pushed || !target.NeedsPush() {
		res.LocalUpdated, err = fastForwardLocal(ctx, workspace, target.Branch, parent, sha, clean)
		if err != nil {
			return res, err
		}
	}
	if !res.LocalUpdated && !res.Pushed {
		side := brand.BranchPrefix + "/agent-files-" + sha[:8]
		if _, err := gitOut(ctx, workspace, nil, "update-ref", "refs/heads/"+side, sha, ""); err != nil {
			return res, fmt.Errorf("keep commit %s on branch %s: %w", sha[:8], side, err)
		}
		res.SideBranch = side
	}
	return res, nil
}

// PushAsBranch pushes commit sha to a new branch on remote, for a pull request
// when the default branch refuses direct pushes. It never forces.
func PushAsBranch(ctx context.Context, workspace, remote, sha string, gitEnv []string) (string, error) {
	if !isHexSHA(sha) {
		return "", errors.New("invalid commit id")
	}
	remotes, err := gitOut(ctx, workspace, nil, "remote")
	if err != nil {
		return "", err
	}
	if !containsLine(remotes, remote) {
		return "", fmt.Errorf("unknown remote %q", remote)
	}
	if _, err := gitOut(ctx, workspace, nil, "rev-parse", "--verify", "-q", sha+"^{commit}"); err != nil {
		return "", fmt.Errorf("commit %s not found", sha)
	}
	branch := brand.BranchPrefix + "/agent-files-" + sha[:8]
	if _, err := gitOut(ctx, workspace, pushEnv(gitEnv), "push", "--", remote, sha+":refs/heads/"+branch); err != nil {
		return "", err
	}
	return branch, nil
}

// checkPaths keeps only clean, checkout-relative paths that exist, are in
// allowed and stay inside the checkout.
func checkPaths(workspace string, paths, allowed []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, ErrNothingToCommit
	}
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		c := path.Clean(filepath.ToSlash(p))
		if p == "" || c != p || path.IsAbs(c) || c == "." || c == ".." || strings.HasPrefix(c, "../") || !ok[c] {
			return nil, fmt.Errorf("%w: %s", ErrPathNotAllowed, p)
		}
		if !destSafe(workspace, c) {
			return nil, fmt.Errorf("%w: %s", errUnsafe, p)
		}
		if _, err := os.Lstat(filepath.Join(workspace, filepath.FromSlash(c))); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out, nil
}

// commitTree writes a commit of parent plus paths using a throwaway index.
func commitTree(ctx context.Context, workspace, parent string, paths []string, message string) (string, error) {
	dir, err := os.MkdirTemp("", "tallpine-skills-index-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(dir, "index")}
	if _, err := gitOut(ctx, workspace, env, "read-tree", parent); err != nil {
		return "", err
	}
	if _, err := gitOut(ctx, workspace, env, append([]string{"add", "--"}, paths...)...); err != nil {
		return "", fmt.Errorf("stage created files: %w", err)
	}
	tree, err := gitOut(ctx, workspace, env, "write-tree")
	if err != nil {
		return "", err
	}
	if base, err := gitOut(ctx, workspace, nil, "rev-parse", parent+"^{tree}"); err == nil && base == tree {
		return "", ErrNothingToCommit
	}
	sha, err := gitOut(ctx, workspace, nil, "commit-tree", tree, "-p", parent, "-m", message)
	if err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return sha, nil
}

// fastForwardLocal moves refs/heads/<branch> from parent to sha when that is
// safe: the branch exists, sits exactly on parent, and is not checked out in
// another worktree. When it is checked out here, the index gains the committed
// paths so the checkout stays clean instead of showing them as deleted.
func fastForwardLocal(ctx context.Context, workspace, branch, parent, sha string, paths []string) (bool, error) {
	ref := "refs/heads/" + branch
	// A missing branch reads as "", which never equals parent.
	if cur, _ := gitOut(ctx, workspace, nil, "rev-parse", "--verify", "-q", ref); cur != parent {
		return false, nil
	}
	here, elsewhere, err := checkedOut(ctx, workspace, ref)
	if err != nil || elsewhere {
		return false, err
	}
	if !gitOK(ctx, workspace, "update-ref", ref, sha, parent) {
		return false, nil // moved under us: leave it alone
	}
	if here {
		if _, err := gitOut(ctx, workspace, nil, append([]string{"add", "--"}, paths...)...); err != nil {
			return true, fmt.Errorf("committed %s, but updating the checkout's index failed: %w", sha[:8], err)
		}
	}
	return true, nil
}

// checkedOut reports whether ref is the checked-out branch of the workspace
// checkout or of any other worktree of the repository.
func checkedOut(ctx context.Context, workspace, ref string) (here, elsewhere bool, err error) {
	top, err := gitOut(ctx, workspace, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return false, false, err
	}
	list, err := gitOut(ctx, workspace, nil, "worktree", "list", "--porcelain")
	if err != nil {
		return false, false, err
	}
	var wt string
	for _, line := range strings.Split(list, "\n") {
		if v, ok := strings.CutPrefix(line, "worktree "); ok {
			wt = v
		}
		if line == "branch "+ref {
			if samePath(wt, top) {
				here = true
			} else {
				elsewhere = true
			}
		}
	}
	return here, elsewhere, nil
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// pushEnv keeps git from prompting for credentials in the daemon and adds the
// project's GitHub account, if any.
func pushEnv(gitEnv []string) []string {
	return append([]string{"GIT_TERMINAL_PROMPT=0"}, gitEnv...)
}

// gitOut runs git in workspace and returns trimmed stdout; the error carries
// git's stderr.
func gitOut(ctx context.Context, workspace string, env []string, args ...string) (string, error) {
	timeout := gitTimeout
	if len(args) > 0 && args[0] == "push" {
		timeout = 2 * gitTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := process.CommandContext(ctx, "git", append([]string{"-C", workspace}, args...)...) //nolint:gosec // fixed git subcommands on the user's own checkout
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// TrackedAt reports whether rel exists at ref, e.g. whether a path still reaches
// new sessions from the branch they start from.
func TrackedAt(ctx context.Context, workspace, ref, rel string) bool {
	return gitOK(ctx, workspace, "cat-file", "-e", ref+":"+rel)
}

// gitOK runs git and reports whether it succeeded.
func gitOK(ctx context.Context, workspace string, args ...string) bool {
	_, err := gitOut(ctx, workspace, nil, args...)
	return err == nil
}

func isHexSHA(s string) bool {
	if len(s) < 8 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func containsLine(s, want string) bool {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) == want {
			return true
		}
	}
	return false
}
