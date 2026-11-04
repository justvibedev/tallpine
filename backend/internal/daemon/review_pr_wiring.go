package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/observe/scm"
	"tallpine.eu/tallpine/backend/internal/ports"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

// reportedPRClaimer links a PR a worker reported creating to that worker. A
// report only ever links a PR nobody else owns: every other owner, terminated
// ones included, vetoes the claim inside the claim transaction. This is the
// scoped-worker rule of agentClaimTakeoverGuard (ADR 0008), applied whoever
// filed the report, since a report is never a request to take a PR over.
type reportedPRClaimer struct {
	sessions interface {
		ClaimPR(ctx context.Context, id domain.SessionID, ref string, opts sessionsvc.ClaimPROptions) (sessionsvc.ClaimPRResult, error)
	}
}

func (c reportedPRClaimer) Claim(ctx context.Context, workerID domain.SessionID, prURL string) error {
	_, err := c.sessions.ClaimPR(ctx, workerID, prURL, sessionsvc.ClaimPROptions{
		AllowTakeover:            false,
		RefuseOtherSessionBranch: true,
		TakeoverGuard: func(owner ports.ClaimOwner) error {
			return fmt.Errorf("pull request %s belongs to session %s; a report cannot take it over: %w", prURL, owner.SessionID, reportsvc.ErrPRNotLinkable)
		},
	})
	// Only definitive refusals mark the PR as not the worker's; transient
	// failures (provider down, timeout) keep it as reported.
	if errors.Is(err, sessionsvc.ErrPRNotOpen) || errors.Is(err, sessionsvc.ErrPRBranchNotSession) {
		return fmt.Errorf("%w: %w", reportsvc.ErrPRNotLinkable, err)
	}
	return err
}

// reviewPRRefresher lets a review trigger fetch a worker's PR fresh from the
// provider through the session service's claim, which re-reads the PR and
// records it on the session. Claiming a PR the session already owns only
// refreshes its facts; another active session's PR is never taken over.
type reviewPRRefresher struct {
	sessions interface {
		ClaimPR(ctx context.Context, id domain.SessionID, ref string, opts sessionsvc.ClaimPROptions) (sessionsvc.ClaimPRResult, error)
	}
	// store and scm enable DiscoverPRs; both nil disables discovery.
	store interface {
		GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
		GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	}
	scm interface {
		ParseRepository(remote string) (ports.SCMRepo, bool)
		ListPRsByRepo(ctx context.Context, repo ports.SCMRepo, updatedAfter time.Time) ([]ports.SCMPRObservation, error)
		AuthenticatedIdentityForProvider(ctx context.Context, provider, host string) (ports.SCMIdentity, error)
	}
}

// DiscoverPRs attaches the session's open PRs from any branch in its
// namespace (a follow-up PR on a second branch) without waiting for the SCM
// observer's next poll. Only branches the session owns are claimed.
func (r reviewPRRefresher) DiscoverPRs(ctx context.Context, workerID domain.SessionID) error {
	if r.store == nil || r.scm == nil {
		return nil
	}
	sess, ok, err := r.store.GetSession(ctx, workerID)
	if err != nil || !ok {
		return err
	}
	project, ok, err := r.store.GetProject(ctx, string(sess.ProjectID))
	if err != nil || !ok {
		return err
	}
	repo, ok := r.scm.ParseRepository(cmp.Or(project.Config.CanonicalRepoURL, project.RepoOriginURL))
	if !ok {
		return nil
	}
	id, err := r.scm.AuthenticatedIdentityForProvider(ctx, repo.Provider, repo.Host)
	if err != nil {
		return err
	}
	pulls, err := r.scm.ListPRsByRepo(ctx, repo, time.Time{})
	if err != nil {
		return err
	}
	workspace := project.Kind.WithDefault() == domain.ProjectKindWorkspace
	var errs []error
	for _, pr := range pulls {
		if pr.Merged || pr.Closed || !scm.DiscoverableByIdentity(pr, repo, id) || !scm.SessionOwnsBranch(sess, workspace, pr.SourceBranch) {
			continue
		}
		if err := r.RefreshPR(ctx, workerID, cmp.Or(pr.HTMLURL, pr.URL)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r reviewPRRefresher) RefreshPR(ctx context.Context, workerID domain.SessionID, prURL string) error {
	_, err := r.sessions.ClaimPR(ctx, workerID, prURL, sessionsvc.ClaimPROptions{AllowTakeover: false, RequireSessionBranch: true})
	var owned ports.PRClaimedByActiveSessionError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &owned):
		return fmt.Errorf("%w: %s belongs to active session %s", reviewcore.ErrPROwnedElsewhere, prURL, owned.Owner)
	case errors.Is(err, sessionsvc.ErrPRNotFound):
		return fmt.Errorf("%w: pull request %s was not found on the provider", reviewcore.ErrNotFound, prURL)
	case errors.Is(err, sessionsvc.ErrPRBranchNotSession):
		return fmt.Errorf("%w: pull request %s head branch does not belong to this session; claim it explicitly with `tallpine session claim-pr` if it is yours", reviewcore.ErrInvalid, prURL)
	case errors.Is(err, sessionsvc.ErrPRNotOpen):
		return fmt.Errorf("%w: pull request %s is not open", reviewcore.ErrInvalid, prURL)
	case errors.Is(err, sessionsvc.ErrInvalidPRRef), errors.Is(err, sessionsvc.ErrProjectMismatch):
		return fmt.Errorf("%w: %s is not a pull request in this project's repository", reviewcore.ErrInvalid, prURL)
	case errors.Is(err, sessionsvc.ErrSCMUnavailable):
		return fmt.Errorf("%w: Tallpine could not fetch %s from the provider; try again shortly", reviewcore.ErrInvalid, prURL)
	default:
		return err
	}
}

// reviewerLaunchAuth checks a Claude reviewer's login in its account's env with
// the same validator the worker launch uses.
type reviewerLaunchAuth struct{ agents ports.AgentResolver }
