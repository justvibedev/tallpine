package session

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// GetWorkspaceHistory loads commit and upstream metadata without paying for
// the legacy all-files inventory used by the full workspace browser. The zero
// view reads the checked-out worktree; a branch or pull request view reads
// that view's revisions only.
func (s *Service) GetWorkspaceHistory(ctx context.Context, id domain.SessionID, view WorkspaceView) (WorkspaceHistory, error) {
	resolved, err := s.resolveWorkspaceView(ctx, id, view)
	if err != nil {
		return WorkspaceHistory{}, err
	}
	if resolved.Kind != WorkspaceViewCheckedOut {
		return workspaceViewHistory(ctx, id, resolved)
	}
	if resolved.root == "" {
		return WorkspaceHistory{SessionID: id, View: resolved.WorkspaceViewInfo, Commits: []CommitSummary{}}, nil
	}
	var commits workspaceCommits
	var ahead, behind *int
	var landed bool
	g, gctx := errgroup.WithContext(ctx)
	if resolved.rangeBefore != "" {
		g.Go(func() (err error) {
			commits.list, commits.truncated, err = gitCommitLog(gctx, resolved.root, resolved.rangeBefore)
			return err
		})
	}
	g.Go(func() error {
		ahead, behind = gitAheadBehind(gctx, resolved.root)
		return nil
	})
	g.Go(func() error {
		landed = s.workspaceHeadLanded(gctx, id, resolved.root)
		return nil
	})
	if err := g.Wait(); err != nil {
		return WorkspaceHistory{}, err
	}
	if commits.list == nil {
		commits.list = []CommitSummary{}
	}
	return WorkspaceHistory{SessionID: id, View: resolved.WorkspaceViewInfo, Commits: commits.list, CommitsTruncated: commits.truncated, Ahead: ahead, Behind: behind, Landed: landed}, nil
}

// workspaceHeadLanded is gitHeadLanded against the session's recorded base.
// An unreadable session reads as not landed.
func (s *Service) workspaceHeadLanded(ctx context.Context, id domain.SessionID, root string) bool {
	rec, err := s.sessionWorkspaceRecord(ctx, id)
	if err != nil {
		return false
	}
	return gitHeadLanded(ctx, root, rec.Metadata.DiffBaseRef, rec.Metadata.DiffBaseSHA)
}

func workspaceDegradedCode(err error) string {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) && apiErr.Code != "" {
		return apiErr.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "WORKSPACE_GIT_TIMEOUT"
	}
	return "WORKSPACE_GIT_STATE_UNAVAILABLE"
}

func workspaceCommit(files WorkspaceFiles, rawSHA string) (CommitSummary, error) {
	sha := strings.TrimSpace(rawSHA)
	for _, commit := range files.Commits {
		if commit.SHA == sha {
			return commit, nil
		}
	}
	return CommitSummary{}, apierr.NotFound("WORKSPACE_COMMIT_NOT_FOUND", "Commit is not available in this workspace comparison")
}

// workspaceCommits is the commit list between base and HEAD, newest first,
// and whether older commits were left out of it.
type workspaceCommits struct {
	list      []CommitSummary
	truncated bool
}

// gitUntrackedFiles lists untracked, non-ignored files in the worktree.
func gitUntrackedFiles(ctx context.Context, root string) ([]string, error) {
	out, err := gitWorkspaceOutput(ctx, root, "ls-files", "-z", "-o", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// gitCommitLog lists the commits reachable from HEAD but not base, newest
// first, up to maxCommitLogCommits of them. truncated reports that older
// commits were left out.
func gitCommitLog(ctx context.Context, root, base string) ([]CommitSummary, bool, error) {
	commits, changes, counts, truncated, err := gitCommitLogChanges(ctx, root, base+"..HEAD", maxCommitLogCommits, maxCommitLogBytes)
	if err != nil {
		return nil, false, err
	}
	for i := range commits {
		change := changes[commits[i].SHA]
		commits[i].Files = buildSectionSummaries(root, change.statuses, counts[commits[i].SHA], change.previous)
	}
	return commits, truncated, nil
}

// gitCommitLogChanges is gitCommitLogChangesArgs for one revision range.
func gitCommitLogChanges(ctx context.Context, root, revRange string, maxCommits, maxBytes int) ([]CommitSummary, map[string]commitChangeSet, map[string]map[string][2]int, bool, error) {
	return gitCommitLogChangesArgs(ctx, root, []string{revRange}, maxCommits, maxBytes)
}

// gitCommitLogChangesArgs lists up to maxCommits commits selected by revArgs
// (a range, or e.g. --first-parent and a tip), newest first, with each
// commit's name-status changes and numstat counts. Both are collected in two
// Git passes, rather than spawning Git once or twice for every commit, and
// each pass keeps at most maxBytes of output. A commit either fits whole in
// both passes or is left out, never listed with part of its files. truncated
// reports that older commits were left out.
func gitCommitLogChangesArgs(ctx context.Context, root string, revArgs []string, maxCommits, maxBytes int) ([]CommitSummary, map[string]commitChangeSet, map[string]map[string][2]int, bool, error) {
	// Reading one commit past the cap tells a full list from a truncated one.
	maxCount := "--max-count=" + strconv.Itoa(maxCommits+1)
	statusArgs := append([]string{"log", maxCount, "--format=%x1e%H%x1f%s%x1f%an%x1f%aI", "--name-status", "--find-renames", "-z"}, revArgs...)
	numstatArgs := append([]string{"log", maxCount, "--format=%x1e%H", "--numstat", "--find-renames", "-z"}, revArgs...)
	var statusOutput, numstatOutput string
	var statusCapped, numstatCapped bool
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		statusOutput, statusCapped, err = gitWorkspaceOutputCapped(gctx, root, maxBytes, statusArgs...)
		return err
	})
	g.Go(func() (err error) {
		numstatOutput, numstatCapped, err = gitWorkspaceOutputCapped(gctx, root, maxBytes, numstatArgs...)
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, nil, nil, false, err
	}
	commits, changes := parseCommitStatusLog(wholeCommitChunks(statusOutput, statusCapped))
	counts := parseCommitNumstatLog(wholeCommitChunks(numstatOutput, numstatCapped))
	truncated := statusCapped || numstatCapped
	if numstatCapped {
		// Past the numstat pass's last whole commit, text files would read as
		// binary for want of counts.
		for i, commit := range commits {
			if _, ok := counts[commit.SHA]; !ok {
				commits = commits[:i]
				break
			}
		}
	}
	if len(commits) > maxCommits {
		commits = commits[:maxCommits]
		truncated = true
	}
	return commits, changes, counts, truncated, nil
}

// wholeCommitChunks drops the commit a capped git log pass stopped partway
// through. Every commit's chunk starts with \x1e, so all output before the
// last one is whole.
func wholeCommitChunks(out string, capped bool) string {
	if !capped {
		return out
	}
	if end := strings.LastIndexByte(out, '\x1e'); end >= 0 {
		return out[:end]
	}
	return ""
}

type commitChangeSet struct {
	statuses map[string]WorkspaceFileStatus
	previous map[string]string
}

func parseCommitStatusLog(out string) ([]CommitSummary, map[string]commitChangeSet) {
	chunks := strings.Split(out, "\x1e")
	commits := make([]CommitSummary, 0, len(chunks)-1)
	changes := make(map[string]commitChangeSet, len(chunks)-1)
	for _, chunk := range chunks[1:] {
		headerEnd := strings.IndexByte(chunk, 0)
		if headerEnd < 0 {
			continue
		}
		fields := strings.Split(chunk[:headerEnd], "\x1f")
		if len(fields) < 4 {
			continue
		}
		timestamp, _ := time.Parse(time.RFC3339, fields[3])
		commit := CommitSummary{SHA: fields[0], Subject: fields[1], Author: fields[2], Timestamp: timestamp}
		statuses, previous := parseNameStatusOutput(strings.TrimLeft(chunk[headerEnd+1:], "\r\n"))
		commits = append(commits, commit)
		changes[commit.SHA] = commitChangeSet{statuses: statuses, previous: previous}
	}
	return commits, changes
}

func parseCommitNumstatLog(out string) map[string]map[string][2]int {
	result := map[string]map[string][2]int{}
	for _, chunk := range strings.Split(out, "\x1e")[1:] {
		headerEnd := strings.IndexByte(chunk, 0)
		if headerEnd < 0 {
			continue
		}
		sha := strings.TrimSpace(chunk[:headerEnd])
		if sha == "" {
			continue
		}
		result[sha] = parseNumstatOutput(strings.TrimLeft(chunk[headerEnd+1:], "\x00\r\n"))
	}
	return result
}

// gitAheadBehind reports HEAD's commit counts against its upstream, falling
// back to origin/<branch> when no upstream is configured. Both return values
// are nil when neither can be resolved (detached HEAD, no remote, git
// failure) — that means "no push/pull data," not an error worth propagating.
// gitHeadLanded reports whether HEAD is contained in baseRef using local refs
// only. A HEAD still at baseSHA (the session's creation commit) is not landed,
// and any failure or empty baseRef reads as not landed.
func gitHeadLanded(ctx context.Context, root, baseRef, baseSHA string) bool {
	baseRef = strings.TrimSpace(baseRef)
	// "HEAD" is the spawn fallback when no default branch resolved: it is not a
	// real base, and HEAD is trivially contained in itself.
	if baseRef == "" || baseRef == "HEAD" || baseRef == "@" {
		return false
	}
	head, err := gitWorkspaceOutput(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil || strings.TrimSpace(head) == "" || strings.TrimSpace(head) == strings.TrimSpace(baseSHA) {
		return false
	}
	out, err := gitWorkspaceOutput(ctx, root, "rev-list", "-n", "1", "HEAD", "--not", baseRef, "--")
	return err == nil && strings.TrimSpace(out) == ""
}

func gitAheadBehind(ctx context.Context, root string) (*int, *int) {
	ref, err := gitWorkspaceOutput(ctx, root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	upstream := strings.TrimSpace(ref)
	if err != nil || upstream == "" {
		branchOut, branchErr := gitWorkspaceOutput(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
		branch := strings.TrimSpace(branchOut)
		if branchErr != nil || branch == "" || branch == "HEAD" {
			return nil, nil
		}
		candidate := "origin/" + branch
		if !gitCommitExists(ctx, root, candidate) {
			return nil, nil
		}
		upstream = candidate
	}
	out, err := gitWorkspaceOutput(ctx, root, "rev-list", "--left-right", "--count", "HEAD..."+upstream)
	if err != nil {
		return nil, nil
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 2 {
		return nil, nil
	}
	aheadCount, aheadErr := strconv.Atoi(fields[0])
	behindCount, behindErr := strconv.Atoi(fields[1])
	if aheadErr != nil || behindErr != nil {
		return nil, nil
	}
	return &aheadCount, &behindCount
}
