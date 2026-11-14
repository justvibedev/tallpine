package session

import (
	"context"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// settledVerdict folds one verdict per reviewer into one. Any request for
// changes wins; approval needs every reviewer to have approved, so a pass
// still running or without a verdict keeps the head undecided.
func settledVerdict(verdicts []domain.ReviewVerdict) domain.ReviewVerdict {
	approved := false
	for _, v := range verdicts {
		switch v {
		case domain.VerdictChangesRequested:
			return domain.VerdictChangesRequested
		case domain.VerdictApproved:
			approved = true
		default:
			return domain.VerdictNone
		}
	}
	if approved {
		return domain.VerdictApproved
	}
	return domain.VerdictNone
}

// prReviewVerdict is the verdict of the passes against the PR heads.
func prReviewVerdict(runs []domain.CurrentHeadReviewRun) domain.ReviewVerdict {
	verdicts := make([]domain.ReviewVerdict, 0, len(runs))
	for _, run := range runs {
		verdicts = append(verdicts, runVerdict(run.Status, run.Verdict))
	}
	return settledVerdict(verdicts)
}

func runVerdict(status domain.ReviewRunStatus, verdict domain.ReviewVerdict) domain.ReviewVerdict {
	if status != domain.ReviewRunComplete && status != domain.ReviewRunDelivered {
		return domain.VerdictNone
	}
	return verdict
}

// branchReviewVerdict is what review decided about the session's local branch
// at its current tip. It uses the land gate's rule (domain.NewestBranchVerdictRun
// on the branch's review key), so a row reads "waiting on you" exactly when
// `tallpine land` would accept the branch; a verdict for an older tip is stale.
func branchReviewVerdict(runs []domain.ReviewRun, rec domain.SessionRecord) domain.ReviewVerdict {
	if rec.BranchState == nil || rec.BranchState.Head == "" {
		return domain.VerdictNone
	}
	latest := domain.NewestBranchVerdictRun(runs, domain.LocalReviewTargetPrefix+strings.TrimSpace(rec.Metadata.Branch))
	switch {
	case latest == nil || strings.TrimSpace(latest.TargetSHA) != rec.BranchState.Head:
		return domain.VerdictNone
	case latest.Verdict == domain.VerdictApproved:
		return domain.VerdictApproved
	}
	return domain.VerdictChangesRequested
}

// branchVerdictLister is the store's batched read of branch review verdicts.
type branchVerdictLister interface {
	ListBranchReviewVerdictsForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.ReviewRun, error)
}

// loadBranchReviewRuns reads the verdict-bearing branch review passes of every
// live session in one query, next to the other batched per-session facts.
func (s *Service) loadBranchReviewRuns(ctx context.Context, recs []domain.SessionRecord) map[domain.SessionID][]domain.ReviewRun {
	lister, ok := s.store.(branchVerdictLister)
	if !ok {
		return nil
	}
	ids := make([]domain.SessionID, 0, len(recs))
	for _, rec := range recs {
		if !rec.IsTerminated {
			ids = append(ids, rec.ID)
		}
	}
	runs, err := lister.ListBranchReviewVerdictsForSessions(ctx, ids)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("list branch review verdicts", "err", err)
		}
		return nil
	}
	return runs
}

// reviewAndLand derives the session's review verdict for its current head and
// whether its work reached the base. branchRuns are the session's branch
// review passes (used only without a PR).
func reviewAndLand(rec domain.SessionRecord, prs []domain.PRFacts, runs []domain.CurrentHeadReviewRun, branchRuns []domain.ReviewRun) (domain.ReviewVerdict, bool) {
	landed := rec.BranchState != nil && rec.BranchState.Landed
	for _, pr := range prs {
		landed = landed || pr.Merged
	}
	switch {
	case rec.IsTerminated:
		return domain.VerdictNone, landed
	case len(prs) > 0:
		return prReviewVerdict(runs), landed
	case landed:
		return domain.VerdictNone, landed
	}
	return branchReviewVerdict(branchRuns, rec), landed
}
