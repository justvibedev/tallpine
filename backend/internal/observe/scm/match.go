package scm

import (
	"context"
	"strconv"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

// resolveIdentities resolves each provider/host once per poll. Unknown or bot
// accounts disable discovery only for that scope; other accounts keep working.
func (o *Observer) resolveIdentities(ctx context.Context, sessionRepos []sessionRepo) map[string]ports.SCMIdentity {
	if o.scopedIdentityResolver == nil {
		return nil
	}
	seen := map[string]bool{}
	identities := map[string]ports.SCMIdentity{}
	for _, sr := range sessionRepos {
		ik := identityKey(sr.repo.Provider, sr.repo.Host)
		if seen[ik] {
			continue
		}
		seen[ik] = true
		id, err := o.scopedIdentityResolver.AuthenticatedIdentityForProvider(ctx, sr.repo.Provider, sr.repo.Host)
		if err != nil {
			o.logger.Debug("scm observer: identity unavailable; automatic discovery disabled for scope", "provider", sr.repo.Provider, "host", sr.repo.Host, "err", err)
			continue
		}
		id.Login = strings.TrimSpace(id.Login)
		if !id.Human || id.Login == "" {
			continue
		}
		identities[ik] = id
	}
	return identities
}

func candidatesForHeadRepo(candidates []sessionRepo, headRepo string) []sessionRepo {
	name := strings.ToLower(strings.TrimSpace(headRepo))
	if name == "" {
		return nil
	}
	var out []sessionRepo
	for _, sr := range candidates {
		if sr.trackOnly {
			continue
		}
		if _, configured := sr.headNames[name]; configured {
			out = append(out, sr)
		}
	}
	return out
}

// matchSession prefers exact branches, then the longest owned prefix. Root
// leaves own slash siblings. Legacy workspace branches are bare refs, so they
// also own hyphen siblings, but only under the validated Tallpine session branch.
// Equal specificity across different sessions is ambiguous: leave the PR for
// explicit claiming rather than assigning it according to store iteration order.
func matchSession(candidates []sessionRepo, sourceBranch string) (sessionRepo, bool) {
	best, ok, _ := matchSessionDetail(candidates, sourceBranch)
	return best, ok
}

// matchSessionDetail is matchSession that also reports whether the branch
// matched several sessions equally, which must never fall through to guessing.
func matchSessionDetail(candidates []sessionRepo, sourceBranch string) (sr sessionRepo, ok, ambiguous bool) {
	var best sessionRepo
	bestLen := -1
	consider := func(sr sessionRepo, length int) {
		if length > bestLen {
			best, bestLen, ambiguous = sr, length, false
		} else if length == bestLen && best.session.ID != sr.session.ID {
			ambiguous = true
		}
	}
	for _, sr := range candidates {
		if sr.branch == "" {
			continue
		}
		// A renamed task keeps owning its temporary branch, which the agent may
		// have built on. It ranks below any session's current branch, so a later
		// task given that name still wins its own PRs.
		for temporary, owned := range []string{sr.branch, sr.session.TemporaryBranch} {
			if owned == "" {
				continue
			}
			rank := func(length int) int { return 2*length + 1 - temporary }
			if owned == sourceBranch {
				consider(sr, rank(len(sourceBranch)+1))
			}
			for _, prefix := range sessionBranchPrefixes(owned) {
				if prefix == sourceBranch || strings.HasPrefix(sourceBranch, prefix+"/") {
					consider(sr, rank(len(prefix)))
				}
			}
			if workspaceHyphenBranch(sr, owned) && strings.HasPrefix(sourceBranch, owned+"-") && len(sourceBranch) > len(owned)+1 {
				consider(sr, rank(len(owned)))
			}
		}
	}
	return best, bestLen >= 0 && !ambiguous, ambiguous
}

// workspaceBranchInfo records which branches a session's own worktree has been
// on: its checked-out branch and every branch its HEAD reflog records switching
// to or from. A worktree's HEAD reflog is private to that worktree even though
// refs/heads is shared, so a branch listed here was created or used by this
// session and by no other.
type workspaceBranchInfo struct {
	current string
	visited map[string]struct{}
	// reflogRead is false when git could not read the HEAD reflog at all.
	reflogRead bool
}

const workspaceReflogLimit = "300"

func readWorkspaceBranches(ctx context.Context, path string) workspaceBranchInfo {
	ctx, cancel := context.WithTimeout(ctx, gitOriginTimeout)
	defer cancel()
	info := workspaceBranchInfo{visited: map[string]struct{}{}}
	if out, err := tallpineprocess.CommandContext(ctx, "git", "-C", path, "symbolic-ref", "--short", "-q", "HEAD").Output(); err == nil {
		info.current = strings.TrimSpace(string(out))
	}
	out, err := tallpineprocess.CommandContext(ctx, "git", "-C", path, "reflog", "show", "--format=%gs", "-n", workspaceReflogLimit, "HEAD", "--").Output()
	if err != nil {
		return info
	}
	info.reflogRead = true
	for _, line := range strings.Split(string(out), "\n") {
		// "checkout: moving from <old> to <new>"; a commit SHA on either side is a
		// detached HEAD and simply never equals a PR source branch.
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "checkout: moving from ")
		if !ok {
			continue
		}
		from, to, ok := strings.Cut(rest, " to ")
		if !ok {
			continue
		}
		info.visited[strings.TrimSpace(from)] = struct{}{}
		info.visited[strings.TrimSpace(to)] = struct{}{}
	}
	return info
}

var readWorkspaceBranchesFunc = readWorkspaceBranches

// WorkspaceVisitedBranches returns the branches a worktree's HEAD reflog shows
// it on; ok is false when the reflog could not be read.
func WorkspaceVisitedBranches(ctx context.Context, path string) (visited map[string]struct{}, ok bool) {
	info := readWorkspaceBranchesFunc(ctx, path)
	return info.visited, info.reflogRead
}

// branchCreatedLocally reports whether branch was created by a local
// `git branch`/`checkout -b`/`switch -c` rather than fetched or pulled in (as
// `gh pr checkout` does for someone else's PR). The oldest entry of the
// branch's own reflog says how it came to exist. A missing or expired reflog
// answers false: unknown provenance is never evidence of authorship.
func branchCreatedLocally(ctx context.Context, path, branch string) bool {
	ctx, cancel := context.WithTimeout(ctx, gitOriginTimeout)
	defer cancel()
	out, err := tallpineprocess.CommandContext(ctx, "git", "-C", path, "reflog", "show", "--format=%gs", "refs/heads/"+branch, "--").Output()
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	startPoint, ok := strings.CutPrefix(strings.TrimSpace(lines[len(lines)-1]), "branch: Created from ")
	if !ok {
		return false
	}
	// `gh pr checkout` of a same-repo PR runs `checkout -b <b> --track
	// <remote>/<b>`, which also logs "Created from". A start point that is the
	// remote-tracking ref of the same branch name means someone else's branch
	// was copied down, not authored here.
	startPoint = strings.TrimSpace(startPoint)
	if remote, ok := strings.CutPrefix(startPoint, "refs/remotes/"); ok {
		_, name, _ := strings.Cut(remote, "/")
		return name != branch
	}
	if remote, ok := strings.CutSuffix(startPoint, "/"+branch); ok && remote != "" && !strings.Contains(remote, "/") {
		return false
	}
	return true
}

var branchCreatedLocallyFunc = branchCreatedLocally

// matchWorkspaceBranch attributes a PR whose head branch matches no session's
// naming scheme to the one session whose worktree is on, or has been on, that
// branch, provided the branch was created locally. Reflogs also record
// branches a session merely checked out to inspect, so provenance is required
// on top of uniqueness. It refuses when the claim is shared (for example two
// worktrees that both started from the same feature branch), so it never links
// a PR to a neighbouring session. Workspace projects have no single worktree to
// read and are skipped.
func matchWorkspaceBranch(ctx context.Context, candidates []sessionRepo, sourceBranch string, cache map[string]workspaceBranchInfo) (sessionRepo, bool) {
	var current, visited []sessionRepo
	seen := map[domain.SessionID]bool{}
	for _, sr := range candidates {
		path := strings.TrimSpace(sr.session.Metadata.WorkspacePath)
		if sr.workspace || path == "" || seen[sr.session.ID] {
			continue
		}
		seen[sr.session.ID] = true
		info, ok := cache[path]
		if !ok {
			info = readWorkspaceBranchesFunc(ctx, path)
			cache[path] = info
		}
		if info.current == sourceBranch {
			current = append(current, sr)
		} else if _, ok := info.visited[sourceBranch]; ok {
			visited = append(visited, sr)
		}
	}
	// A worktree standing on the branch beats ones that only passed through it.
	for _, group := range [][]sessionRepo{current, visited} {
		if len(group) == 1 {
			owner := group[0]
			if !branchCreatedLocallyFunc(ctx, strings.TrimSpace(owner.session.Metadata.WorkspacePath), sourceBranch) {
				return sessionRepo{}, false
			}
			return owner, true
		}
		if len(group) > 1 {
			return sessionRepo{}, false
		}
	}
	return sessionRepo{}, false
}

func workspaceHyphenBranch(sr sessionRepo, branch string) bool {
	if !sr.workspace || sr.session.ID == "" {
		return false
	}
	// Sessions created before the rebrand own tp/<id> branches; new ones tp/<id>.
	var suffix string
	matched := false
	for _, prefix := range append(brand.BranchPrefixes(), brand.LegacyBranchPrefix) {
		base := prefix + "/" + string(sr.session.ID)
		if branch == base {
			return true
		}
		if s, ok := strings.CutPrefix(branch, base+"-"); ok {
			suffix, matched = s, true
			break
		}
	}
	if !matched {
		return false
	}
	// workspaceProjectBranch appends -2, -3, ... when a ref already exists.
	// Do not treat arbitrary topics, another session ID, or padded numbers as
	// a generated branch and broaden their ownership.
	n, err := strconv.Atoi(suffix)
	return err == nil && n >= 2 && strconv.Itoa(n) == suffix
}

func sessionBranchPrefixes(branch string) []string {
	prefixes := []string{branch}
	if namespace, ok := strings.CutSuffix(branch, "/root"); ok && namespace != "" {
		prefixes = append(prefixes, namespace)
	}
	return prefixes
}

// canAdoptOrigin reports whether a live origin may replace the stored one. An
// empty or unparseable stored URL is backfilled; otherwise only a rename on the
// same provider and host is followed, so a retargeted remote cannot move the
// project's trusted repository.
func canAdoptOrigin(stored, live string) bool {
	old, err := domain.ParseRepositoryIdentity(stored)
	if err != nil {
		return true
	}
	next, err := domain.ParseRepositoryIdentity(live)
	return err == nil && old.Provider == next.Provider && old.Host == next.Host
}

// gitOriginTimeout bounds the origin probe so a hung mount or slow git cannot
// block the caller.
const gitOriginTimeout = 5 * time.Second

// resolveGitOriginURL runs `git -C path remote get-url origin` and returns the
// trimmed URL, or "" if the command fails (missing repo, no origin remote, etc).
// The observer uses this to backfill projects that were registered before
// project.Add resolved origin URLs at add time.
func resolveGitOriginURL(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitOriginTimeout)
	defer cancel()
	out, err := tallpineprocess.CommandContext(ctx, "git", "-C", path, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitRemoteURLs(ctx context.Context, path string) []string {
	out, err := tallpineprocess.CommandContext(ctx, "git", "-C", path, "remote").Output()
	if err != nil {
		return nil
	}
	var urls []string
	seen := make(map[string]bool)
	for _, name := range strings.Fields(string(out)) {
		for _, options := range [][]string{{"--all"}, {"--push", "--all"}} {
			args := append([]string{"-C", path, "remote", "get-url"}, options...)
			args = append(args, name)
			output, err := tallpineprocess.CommandContext(ctx, "git", args...).Output()
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(output), "\n") {
				address := strings.TrimSpace(line)
				if address != "" && !seen[address] {
					seen[address] = true
					urls = append(urls, address)
				}
			}
		}
	}
	return urls
}

var gitRemoteURLsFunc = gitRemoteURLs

func scrubLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}
