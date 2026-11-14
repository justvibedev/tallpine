// Package localland lands an approved session branch that has no PR: it
// squashes the branch onto the target branch as one commit, moves the target
// only by compare-and-swap (or a no-force push when the project has a hosted
// remote), and fast-forwards a clean checkout of the target. Lands of one
// project are serialized.
package localland

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/review"
)

var (
	// ErrSessionNotFound is returned when: no such session.
	ErrSessionNotFound = errors.New("session not found")
	// ErrNoBranch is returned when: the session has no branch or project checkout to land.
	ErrNoBranch = errors.New("session has no branch to land")
	// ErrHasPR is returned when: the session tracks a PR; it lands through tallpine pr land.
	ErrHasPR = errors.New("session has a PR")
	// ErrNotApproved is returned when: the branch head has no approved review.
	ErrNotApproved = errors.New("not approved")
	// ErrBadTarget is returned when: the target branch is invalid or missing.
	ErrBadTarget = errors.New("bad target branch")
	// ErrConflict is returned when: the branch does not merge cleanly onto the target.
	ErrConflict = errors.New("conflict")
	// ErrDirtyCheckout is returned when: a checkout of the target has local changes.
	ErrDirtyCheckout = errors.New("target checkout is not clean")
	// ErrTargetMoved is returned when: the target moved while landing.
	ErrTargetMoved = errors.New("target moved")
	// ErrPushRejected is returned when: the remote rejected the push of the target.
	ErrPushRejected = errors.New("push rejected")
)

// Sessions looks sessions up.
type Sessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Store reads the project, tracked PRs and review runs of a session.
type Store interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListPRsBySession(ctx context.Context, id domain.SessionID) ([]domain.PullRequest, error)
	ListReviewRunsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error)
}

// DefaultBranchResolver is the resolver spawn uses for the project default.
type DefaultBranchResolver interface {
	ResolveDefaultBranch(ctx context.Context, repoPath, configuredBranch string) (ports.WorkspaceDefaultBranch, error)
}

// Request selects the target and whether to move anything.
type Request struct {
	Into   string
	DryRun bool
}

// Result describes the land.
type Result struct {
	Target           string
	OldSHA           string
	NewSHA           string
	Pushed           bool
	UpdatedCheckouts []string
	AlreadyLanded    bool
	DryRun           bool
	// Notes explains a step skipped after a successful push (local target left).
	Notes []string
}

// Service lands session branches.
type Service struct {
	sessions Sessions
	store    Store
	branches DefaultBranchResolver
	log      *slog.Logger

	mu    sync.Mutex
	locks map[domain.ProjectID]*sync.Mutex

	credentials ports.ProjectGitCredentials // optional: fetch and push as the project's account

	beforeUpdate func() // test hook: runs between reading and moving the target
}

// New builds the service.
func New(sessions Sessions, store Store, branches DefaultBranchResolver, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{sessions: sessions, store: store, branches: branches, log: log, locks: map[domain.ProjectID]*sync.Mutex{}}
}

// SetGitCredentials completes daemon wiring; nil keeps the machine's git login.
func (s *Service) SetGitCredentials(credentials ports.ProjectGitCredentials) {
	s.credentials = credentials
}

// projectGitEnv runs one git process as the project's GitHub account.
func (s *Service) projectGitEnv(ctx context.Context, project domain.ProjectID) []string {
	if s.credentials == nil {
		return nil
	}
	return s.credentials.ProjectGitEnv(ctx, project)
}

func (s *Service) lock(project domain.ProjectID) func() {
	s.mu.Lock()
	l, ok := s.locks[project]
	if !ok {
		l = &sync.Mutex{}
		s.locks[project] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// Land squashes the session's approved branch onto the target branch.
func (s *Service) Land(ctx context.Context, id domain.SessionID, req Request) (Result, error) {
	rec, ok, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return Result{}, fmt.Errorf("load session: %w", err)
	}
	if !ok {
		return Result{}, ErrSessionNotFound
	}
	prs, err := s.store.ListPRsBySession(ctx, id)
	if err != nil {
		return Result{}, fmt.Errorf("list PRs: %w", err)
	}
	if len(prs) > 0 {
		return Result{}, fmt.Errorf("%w %s; use tallpine pr land", ErrHasPR, prs[0].URL)
	}
	project, ok, err := s.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return Result{}, fmt.Errorf("load project: %w", err)
	}
	branch := strings.TrimSpace(rec.Metadata.Branch)
	repo := strings.TrimSpace(project.Path)
	if !ok || branch == "" || repo == "" {
		return Result{}, ErrNoBranch
	}
	head, ok, err := revParse(ctx, repo, "refs/heads/"+branch)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, fmt.Errorf("%w: branch %s not found", ErrNoBranch, branch)
	}
	if err := s.checkApproved(ctx, id, branch, head); err != nil {
		return Result{}, err
	}
	target, err := s.target(ctx, project, req.Into)
	if err != nil {
		return Result{}, err
	}

	defer s.lock(rec.ProjectID)()
	hosted := domain.HasHostedRemote(project.RepoOriginURL)
	tipRef := "refs/heads/" + target
	var authEnv []string
	if hosted {
		authEnv = s.projectGitEnv(ctx, rec.ProjectID)
		if _, err := gitEnv(ctx, repo, "", authEnv, "fetch", "--no-tags", "origin", "+refs/heads/"+target+":refs/remotes/origin/"+target); err != nil {
			return Result{}, fmt.Errorf("%w: fetch origin %s: %w", ErrBadTarget, target, err)
		}
		tipRef = "refs/remotes/origin/" + target
	}
	base, ok, err := revParse(ctx, repo, tipRef)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, fmt.Errorf("%w: %s not found", ErrBadTarget, target)
	}
	res := Result{Target: target, OldSHA: base, DryRun: req.DryRun}
	if landed, err := FindLanded(ctx, repo, "", base, head); err != nil {
		return Result{}, err
	} else if landed != "" {
		res.NewSHA, res.AlreadyLanded = landed, true
		return res, nil
	}
	tree, conflicts, err := mergeTree(ctx, repo, base, head)
	if err != nil {
		return Result{}, err
	}
	if len(conflicts) > 0 {
		return Result{}, fmt.Errorf("%w: %s does not merge cleanly onto %s: %s", ErrConflict, branch, target, strings.Join(conflicts, ", "))
	}
	if req.DryRun {
		return res, s.dryRunChecks(ctx, repo, target, base, hosted)
	}
	subject := strings.TrimSpace(rec.DisplayName)
	if subject == "" {
		subject = branch
	}
	msg, err := squashMessage(ctx, repo, subject, base, head, string(id))
	if err != nil {
		return Result{}, err
	}
	commit, err := git(ctx, repo, msg, "commit-tree", tree, "-p", base, "-F", "-")
	if err != nil {
		return Result{}, err
	}
	res.NewSHA = commit
	if !hosted {
		res.UpdatedCheckouts, err = moveLocal(ctx, repo, target, base, commit, s.beforeUpdate)
		if err != nil && len(res.UpdatedCheckouts) == 0 {
			return Result{}, err
		}
		if note := aheadOfOriginNote(ctx, repo, target, commit); note != "" {
			res.Notes = append(res.Notes, note)
		}
		return res, err
	}
	return s.pushAndFollow(ctx, repo, target, commit, res, authEnv)
}

// aheadOfOriginNote says the landed commit was not pushed because origin is
// not a hosted remote. It asks origin itself (not a tracking ref), so it works
// without upstream config; it is empty when there is no origin or it matches.
func aheadOfOriginNote(ctx context.Context, repo, target, commit string) string {
	out, err := git(ctx, repo, "", "ls-remote", "origin", "refs/heads/"+target)
	if err != nil {
		return ""
	}
	remote := ""
	if f := strings.Fields(out); len(f) > 0 {
		remote = f[0]
	}
	if remote == commit {
		return ""
	}
	if remote != "" {
		if n, err := git(ctx, repo, "", "rev-list", "--count", remote+".."+commit); err == nil {
			return fmt.Sprintf("local %s is now %s commit(s) ahead of origin/%s; not pushed because origin is not a hosted remote", target, n, target)
		}
	}
	return fmt.Sprintf("local %s now differs from origin/%s; not pushed because origin is not a hosted remote", target, target)
}

// pushAndFollow pushes the squash with no force, then fast-forwards the local
// target when it is behind the new commit. A rejected push moves nothing.
func (s *Service) pushAndFollow(ctx context.Context, repo, target, commit string, res Result, authEnv []string) (Result, error) {
	if s.beforeUpdate != nil {
		s.beforeUpdate()
	}
	if _, err := gitEnv(ctx, repo, "", authEnv, "push", "--porcelain", "origin", commit+":refs/heads/"+target); err != nil {
		return Result{}, fmt.Errorf("%w: origin %s moved or refused the push: %w", ErrPushRejected, target, err)
	}
	res.Pushed = true
	local, ok, err := revParse(ctx, repo, "refs/heads/"+target)
	if err != nil || !ok {
		return res, nil //nolint:nilerr // pushed; no local branch to follow
	}
	if anc, err := isAncestor(ctx, repo, local, commit); err != nil || !anc {
		res.Notes = append(res.Notes, fmt.Sprintf("local %s (%s) is not behind the landed commit; left unmoved", target, short(local)))
		return res, nil //nolint:nilerr // pushed; the local note explains it
	}
	res.UpdatedCheckouts, err = moveLocal(ctx, repo, target, local, commit, nil)
	if err != nil {
		res.Notes = append(res.Notes, "local "+target+" left unmoved: "+err.Error())
	}
	return res, nil
}

func (s *Service) dryRunChecks(ctx context.Context, repo, target, base string, hosted bool) error {
	if hosted {
		return nil
	}
	wts, err := checkouts(ctx, repo, target)
	if err != nil {
		return err
	}
	for _, wt := range wts {
		if err := checkClean(ctx, wt, base, base); err != nil {
			return err
		}
	}
	return nil
}

// checkApproved requires the newest verdict on the branch to be an approval
// of exactly head.
func (s *Service) checkApproved(ctx context.Context, id domain.SessionID, branch, head string) error {
	runs, err := s.store.ListReviewRunsBySession(ctx, id)
	if err != nil {
		return fmt.Errorf("list reviews: %w", err)
	}
	latest := domain.NewestBranchVerdictRun(runs, review.LocalTargetURL(branch))
	switch {
	case latest == nil:
		return fmt.Errorf("%w: %s has no review verdict; run tallpine review trigger", ErrNotApproved, branch)
	case latest.Verdict != domain.VerdictApproved:
		return fmt.Errorf("%w: the latest review of %s is %s", ErrNotApproved, branch, latest.Verdict)
	case strings.TrimSpace(latest.TargetSHA) != head:
		return fmt.Errorf("%w: head %s differs from approved head %s; re-review", ErrNotApproved, short(head), short(latest.TargetSHA))
	}
	return nil
}

func (s *Service) target(ctx context.Context, project domain.ProjectRecord, into string) (string, error) {
	target := strings.TrimSpace(into)
	if target == "" {
		if s.branches == nil {
			return "", fmt.Errorf("%w: no default branch resolver", ErrBadTarget)
		}
		resolved, err := s.branches.ResolveDefaultBranch(ctx, project.Path, project.Config.WorktreeBaseBranch())
		if err != nil {
			return "", fmt.Errorf("%w: resolve default branch: %w", ErrBadTarget, err)
		}
		target = resolved.Branch
	}
	if _, err := git(ctx, project.Path, "", "check-ref-format", "--branch", target); err != nil || strings.HasPrefix(target, "-") {
		return "", fmt.Errorf("%w: %q", ErrBadTarget, target)
	}
	return target, nil
}
