package review

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// SubmittedReview is one review result supplied by the reviewer CLI.
type SubmittedReview struct {
	RunID          string
	Verdict        domain.ReviewVerdict
	Body           string
	GithubReviewID string
	// ReviewID, when set, is the only review whose runs this submission may
	// record (a scoped reviewer's own review). Every run in a batch is checked
	// before any result is recorded.
	ReviewID string
}

// Submit records a reviewer's result for a specific worker review pass.
func (s *Service) Submit(ctx context.Context, workerID domain.SessionID, runID string, verdict domain.ReviewVerdict, body, githubReviewID string) (domain.ReviewRun, error) {
	runs, err := s.SubmitMany(ctx, workerID, []SubmittedReview{{
		RunID:          runID,
		Verdict:        verdict,
		Body:           body,
		GithubReviewID: githubReviewID,
	}})
	if err != nil {
		return domain.ReviewRun{}, err
	}
	if len(runs) == 0 {
		return domain.ReviewRun{}, fmt.Errorf("%w: no review result submitted", ErrInvalid)
	}
	return runs[0], nil
}

// SubmitMany records one reviewer CLI submission containing results for one or
// more PR-scoped runs. Delivery is scoped to the runs in this submission, so a
// missing/stuck result for another PR in the same trigger cannot block feedback.
func (s *Service) SubmitMany(ctx context.Context, workerID domain.SessionID, reviews []SubmittedReview) ([]domain.ReviewRun, error) {
	if workerID == "" {
		return nil, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if len(reviews) == 0 {
		return nil, fmt.Errorf("%w: at least one review result is required", ErrInvalid)
	}
	if s.store == nil {
		return nil, fmt.Errorf("review service store is not configured")
	}
	if err := s.requireOwnReviewRuns(ctx, workerID, reviews); err != nil {
		return nil, err
	}
	runs := make([]domain.ReviewRun, 0, len(reviews))
	var supersededRunIDs []string
	for _, review := range reviews {
		run, err := s.submitOne(ctx, workerID, review)
		if err != nil {
			// A newer trigger or lifecycle cancellation may have made one queued
			// run terminal while the reviewer was working. That run is no longer
			// submittable, but it must not prevent valid siblings from delivery.
			if errors.Is(err, errRunSuperseded) {
				supersededRunIDs = append(supersededRunIDs, review.RunID)
				continue
			}
			return nil, err
		}
		runs = append(runs, run)
	}
	if len(runs) == 0 {
		if len(supersededRunIDs) > 0 {
			return nil, fmt.Errorf("%w: no submittable review runs in submission (superseded: %s)", ErrInvalid, strings.Join(supersededRunIDs, ", "))
		}
		return nil, fmt.Errorf("%w: no submittable review runs in submission", ErrInvalid)
	}
	// The result reaches the worker through the PR itself: the reviewer posts
	// its review on the provider, and the SCM observer forwards unresolved
	// review comments to the worker like any other reviewer's. Tallpine does not send
	// a second, separate verdict message.
	return runs, nil
}

// requireOwnReviewRuns checks every review-bound submission against its run
// before SubmitMany records anything, so a batch naming one foreign run
// records none.
func (s *Service) requireOwnReviewRuns(ctx context.Context, workerID domain.SessionID, reviews []SubmittedReview) error {
	for _, review := range reviews {
		if review.ReviewID == "" {
			continue
		}
		run, ok, err := s.store.GetReviewRun(ctx, review.RunID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: review run %q", ErrNotFound, review.RunID)
		}
		if run.SessionID != workerID || run.ReviewID != review.ReviewID {
			return fmt.Errorf("%w: review run %q is not a run of review %q", ErrForeignReviewRun, review.RunID, review.ReviewID)
		}
	}
	return nil
}

func (s *Service) submitOne(ctx context.Context, workerID domain.SessionID, review SubmittedReview) (domain.ReviewRun, error) {
	runID := review.RunID
	verdict := review.Verdict
	body := review.Body
	githubReviewID := review.GithubReviewID
	if runID == "" {
		return domain.ReviewRun{}, fmt.Errorf("%w: review run id is required", ErrInvalid)
	}
	if !verdict.Valid() {
		return domain.ReviewRun{}, fmt.Errorf("%w: verdict must be %q or %q", ErrInvalid, domain.VerdictApproved, domain.VerdictChangesRequested)
	}
	if verdict == domain.VerdictChangesRequested && body == "" {
		return domain.ReviewRun{}, fmt.Errorf("%w: a changes_requested review requires a body", ErrInvalid)
	}
	run, ok, err := s.store.GetReviewRun(ctx, runID)
	if err != nil {
		return domain.ReviewRun{}, err
	}
	if !ok {
		return domain.ReviewRun{}, fmt.Errorf("%w: review run %q", ErrNotFound, runID)
	}
	if run.SessionID != workerID {
		return domain.ReviewRun{}, fmt.Errorf("%w: review run %q does not belong to worker %q", ErrInvalid, runID, workerID)
	}

	switch run.Status {
	case domain.ReviewRunRunning:
		session, found, err := s.store.GetSession(ctx, workerID)
		if err != nil {
			return domain.ReviewRun{}, err
		}
		if !found {
			return domain.ReviewRun{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
		}
		updated, err := s.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunComplete, verdict, body, githubReviewID, session.AutoInjectReview)
		if err != nil {
			return domain.ReviewRun{}, err
		}
		if !updated {
			return domain.ReviewRun{}, fmt.Errorf("%w: review run %q is not running", errRunSuperseded, runID)
		}
		run.Status = domain.ReviewRunComplete
		run.Verdict = verdict
		run.Body = body
		run.GithubReviewID = githubReviewID
		run.AutoInjectReview = session.AutoInjectReview
	case domain.ReviewRunComplete:
		if run.Verdict != verdict {
			return domain.ReviewRun{}, fmt.Errorf("%w: review run %q already recorded verdict %q", ErrInvalid, runID, run.Verdict)
		}
		if body != "" && body != run.Body {
			return domain.ReviewRun{}, fmt.Errorf("%w: review run %q already recorded a different body", ErrInvalid, runID)
		}
		if githubReviewID != "" && githubReviewID != run.GithubReviewID {
			return domain.ReviewRun{}, fmt.Errorf("%w: review run %q already recorded GitHub review id %q", ErrInvalid, runID, run.GithubReviewID)
		}
	case domain.ReviewRunDelivered:
		return run, nil
	default:
		return domain.ReviewRun{}, fmt.Errorf("%w: review run %q is not running", errRunSuperseded, runID)
	}
	s.emitReviewNotification(ctx, run)
	// The worker's `review wait` returns the moment the run is complete, so file
	// this run's approval report now rather than on the sweep: its acknowledging
	// turn must not end before the report exists to cover it (B426). The claim
	// and every other notice stay with the sweep; the report ID is stable.
	if run.Verdict == domain.VerdictApproved {
		if notice := s.failureNotice(); notice != nil {
			if err := s.fileApprovalReport(ctx, notice, run); err != nil {
				slog.Default().WarnContext(ctx, "review approval report failed; the sweep retries", "run", run.ID, "err", err)
			}
		}
	}
	s.wakeSweep()
	return run, nil
}

func (s *Service) emitReviewNotification(ctx context.Context, run domain.ReviewRun) {
	if s.notifications == nil {
		return
	}
	session, ok, err := s.store.GetSession(ctx, run.SessionID)
	if err != nil || !ok {
		slog.Default().WarnContext(ctx, "review notification session lookup failed", "session", run.SessionID, "run", run.ID, "err", err)
		return
	}
	intent := ports.NotificationIntent{
		SessionID: session.ID, ProjectID: session.ProjectID, PRURL: run.PRURL,
		SessionDisplayName: session.DisplayName, CreatedAt: s.clock(), SourceKey: "review_run:" + run.ID,
	}
	if run.Verdict == domain.VerdictChangesRequested {
		intent.Type = domain.NotificationReviewChangesRequested
	} else {
		intent.Type = domain.NotificationReviewCompleted
	}
	prs, listErr := s.sessionPRs(ctx, session)
	if listErr == nil {
		for _, pr := range prs {
			if pr.URL == run.PRURL || pr.HTMLURL == run.PRURL {
				intent.PRNumber, intent.PRTitle = pr.Number, pr.Title
				intent.Body = headMovedNote([]domain.PullRequest{pr}, run)
				break
			}
		}
	}
	if err := s.notifications.Notify(ctx, intent); err != nil {
		slog.Default().WarnContext(ctx, "review notification failed", "session", run.SessionID, "run", run.ID, "err", err)
	}
}
