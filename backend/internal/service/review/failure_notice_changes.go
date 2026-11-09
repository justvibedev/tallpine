package review

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	"tallpine.eu/tallpine/backend/internal/textutil"
)

// noticeChangesRequested wakes the worker once for a pass that asked for
// changes. The inline comments reach it through the SCM observer on its own
// poll; this is the direct path, so an idle worker learns of the verdict the
// moment the sweep sees it instead of whenever the provider poll lands, and a
// daemon restart between the verdict and the poll loses nothing: the run stays
// unclaimed until a nudge is actually written. A nudge the guard refuses (the
// worker awaits its user) is retried by the next sweep for a day, then dropped.
// Runs whose worker opted out of review injection, whose PR is no longer open
// whose head has moved on, or whose worker is gone are claimed without a nudge.
func (s *Service) noticeChangesRequested(ctx context.Context, notice *failureNotice, run domain.ReviewRun) (bool, error) {
	claim := func() (bool, error) {
		return notice.store.MarkReviewRunFailureNotified(ctx, run.ID, s.clock())
	}
	if !run.AutoInjectReview || notice.nudger == nil {
		return claim()
	}
	worker, ok, err := s.store.GetSession(ctx, run.SessionID)
	if err != nil {
		return false, err
	}
	if !ok || worker.Kind != domain.KindWorker || worker.IsTerminated {
		return claim()
	}
	pr, found := domain.PullRequest{}, false
	prs, err := s.sessionPRs(ctx, worker)
	if err != nil {
		return false, err
	}
	for _, p := range prs {
		if p.URL == run.PRURL || p.HTMLURL == run.PRURL {
			pr, found = p, true
			break
		}
	}
	if !found || pr.Merged || pr.Closed {
		return claim()
	}
	if pr.HeadSHA != "" && run.TargetSHA != "" && pr.HeadSHA != run.TargetSHA {
		return claim() // the worker has pushed since; this feedback is for an old head
	}
	age := s.clock().Sub(run.CreatedAt)
	if age >= endedRunNoticeRetryWindow {
		return claim() // a backlog from before this notice existed, or a worker that never became nudgeable
	}
	// The observer caches comments on its own poll, which can trail this sweep
	// by seconds, so list only the comments of this run's own GitHub review:
	// anything else is an older review's, and a missing list is not an empty one.
	var threads []string
	if comments, err := s.store.ListPRComments(ctx, run.PRURL); err == nil && run.GithubReviewID != "" {
		for _, c := range comments {
			if c.ReviewID != run.GithubReviewID || c.Resolved || !c.AutoInjectReview {
				continue
			}
			loc := c.File
			if c.Line > 0 {
				loc = fmt.Sprintf("%s:%d", c.File, c.Line)
			}
			threads = append(threads, loc)
		}
	}
	message := changesRequestedMessage(run, pr, threads)
	if reviewcore.IsLocalTarget(run.PRURL) {
		message = localChangesRequestedMessage(run)
	}
	sent, err := notice.nudger.Nudge(ctx, worker.ID, message)
	if err != nil {
		slog.Default().WarnContext(ctx, "review changes-requested nudge failed; will retry", "session", worker.ID, "run", run.ID, "err", err)
		return false, nil
	}
	if !sent {
		return false, nil
	}
	return claim()
}

// changesRequestedMessage is the short wake-up for a worker whose PR got a
// changes_requested verdict: the verdict, a trimmed summary, the PR, and the
// open comment locations.
func changesRequestedMessage(run domain.ReviewRun, pr domain.PullRequest, threads []string) string {
	var b strings.Builder
	b.WriteString(failureSubject(run, pr.Number))
	b.WriteString(" requested changes on ")
	b.WriteString(domain.SanitizeControlChars(run.PRURL))
	b.WriteString(".\n")
	if run.NotPostedToPR() {
		// No inline comment will ever sync, so the review text is all the worker gets.
		b.WriteString("The reviewer could not post this review to the PR, so no inline comments will arrive; the findings are below.\n")
		if body := strings.TrimSpace(run.Body); body != "" {
			b.WriteString("Review:\n")
			b.WriteString(truncateNotice(domain.SanitizeControlChars(body), maxLocalReviewBody))
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "Address them and push. `%s review ls` shows every pass.", brand.CLIName)
		return b.String()
	}
	if body := strings.TrimSpace(run.Body); body != "" {
		b.WriteString("Summary: ")
		b.WriteString(truncateNotice(domain.SanitizeControlChars(body), 600))
		b.WriteString("\n")
	}
	if len(threads) > 0 {
		shown := threads
		if len(shown) > 10 {
			shown = shown[:10]
		}
		b.WriteString("Open inline comments: " + strings.Join(shown, ", "))
		if extra := len(threads) - len(shown); extra > 0 {
			fmt.Fprintf(&b, " (+%d more)", extra)
		}
		b.WriteString(". Each arrives as its own message.\n")
	} else {
		b.WriteString("Its inline comments reach you as the PR syncs; the summary above is the verdict.\n")
	}
	fmt.Fprintf(&b, "Address them, push, and resolve each thread. `%s review ls` shows every pass.", brand.CLIName)
	return b.String()
}

// noticeRunsStarted tells the implementation worker a review began on its head,
// once per pass this trigger created, so it stops waiting blindly and does not
// push over a running review. Delivery of the verdict itself is the existing
// review feedback path. Best effort: a refused or failed nudge only logs.
func (s *Service) noticeRunsStarted(ctx context.Context, workerID domain.SessionID, result reviewcore.TriggerResult) {
	notice := s.failureNotice()
	if notice == nil || notice.nudger == nil {
		return
	}
	runs := result.CreatedRuns
	if len(runs) == 0 && result.Created && result.Run.ID != "" {
		runs = []domain.ReviewRun{result.Run}
	}
	for _, run := range runs {
		if !run.AutoInjectReview {
			continue // the worker opted out of review messages
		}
		sha := run.TargetSHA
		if len(sha) > 9 {
			sha = sha[:9]
		}
		message := fmt.Sprintf("Review running on %s. Don't push to that PR until the verdict arrives; a push makes the pass stale. The result is delivered to you when it completes (`%s review ls` shows its state).",
			textutil.FirstNonBlank(sha, "your PR head"), brand.CLIName)
		if _, err := notice.nudger.Nudge(ctx, workerID, message); err != nil {
			slog.Default().WarnContext(ctx, "review started nudge failed", "session", workerID, "run", run.ID, "err", err)
		}
	}
}

// maxUnreviewedPasses bounds retries after passes that never reached the commit.
const maxUnreviewedPasses = 3

// retryUnsubmittedRun starts one more pass with the same reviewer and model
// when a pass died or went idle without a verdict and is the only pass that
// reviewer has had on this commit. The retry's own failure is announced as
// usual. It reports whether a retry was started.
func (s *Service) retryUnsubmittedRun(ctx context.Context, notice *failureNotice, run domain.ReviewRun) bool {
	failedUnsubmitted := run.Status == domain.ReviewRunFailed && reviewcore.IsUnsubmittedReviewerFailure(run.Body)
	if !failedUnsubmitted && !reviewcore.ReviewerGoneCancel(run) {
		return false
	}
	runs, err := notice.store.ListReviewRunsBySession(ctx, run.SessionID)
	if err != nil {
		return false
	}
	unreviewed := 0
	for _, other := range runs {
		if other.ID == run.ID || other.PRURL != run.PRURL || other.TargetSHA != run.TargetSHA || other.Harness != run.Harness {
			continue
		}
		// A pass that never reached the commit (usage limit, restart, trust
		// dialog) does not use up its one retry, up to maxUnreviewedPasses.
		if (reviewcore.NeverReviewed(other.Body) && other.Status == domain.ReviewRunFailed) || reviewcore.ReviewerGoneCancel(other) {
			unreviewed++
			if unreviewed < maxUnreviewedPasses {
				continue
			}
		}
		return false
	}
	worker, ok, err := s.store.GetSession(ctx, run.SessionID)
	if err != nil || !ok || worker.Kind != domain.KindWorker || worker.IsTerminated {
		return false
	}
	prs, err := s.sessionPRs(ctx, worker)
	if err != nil {
		return false
	}
	current := false
	for _, pr := range prs {
		if (pr.URL == run.PRURL || pr.HTMLURL == run.PRURL) && pr.HeadSHA == run.TargetSHA {
			current = true
		}
	}
	if !current {
		return false
	}
	// Bound the retry so a held worker lock or a hung spawn cannot stall the sweep.
	retryCtx, cancel := context.WithTimeout(ctx, sweepReconcileTimeout)
	defer cancel()
	source := run.TriggerSource
	if source == "" {
		source = domain.ReviewTriggerAuto
	}
	result, err := s.triggerWithOptions(retryCtx, run.SessionID, reviewcore.TriggerOptions{
		Harness: run.Harness, Config: domain.AgentConfig{Model: run.Model},
		Source: source, PRURL: run.PRURL, RetryOfRunID: run.ID,
	})
	if err != nil {
		slog.Default().WarnContext(ctx, "review retry after an unsubmitted pass failed", "session", run.SessionID, "run", run.ID, "err", err)
		return false
	}
	if !result.Created && len(result.CreatedRuns) == 0 {
		// The reviewer is busy with another pass (for example the first of two
		// dead passes on one worker): park this one, so it resumes when the
		// reviewer is free and its failure is not announced as final.
		if result.SkipReason == "" && s.reviewerBusyOnAnotherHead(ctx, run) {
			q := domain.QueuedReviewTrigger{SessionID: run.SessionID, PRURL: run.PRURL, Harness: run.Harness, Model: run.Model, TriggerSource: source}
			if err := s.engine.QueueRerun(ctx, q); err == nil {
				return true
			}
		}
		// Skipped by policy or nothing eligible: nothing retried, so announce the failure.
		return false
	}
	slog.Default().InfoContext(ctx, "retried a review pass that ended without a verdict", "session", run.SessionID, "run", run.ID)
	return true
}

// reviewerBusyOnAnotherHead reports whether the run's reviewer is busy with a
// pass that does not cover the run's own head, so a rerun parked now is not
// made redundant by that pass.
func (s *Service) reviewerBusyOnAnotherHead(ctx context.Context, run domain.ReviewRun) bool {
	if s.engine == nil {
		return false
	}
	running, err := s.engine.RunningFor(ctx, run.SessionID, run.Harness)
	if err != nil || len(running) == 0 {
		return false
	}
	for _, other := range running {
		if other.PRURL == run.PRURL && other.TargetSHA == run.TargetSHA {
			return false
		}
	}
	return true
}

// maxLocalReviewBody bounds the review body carried to a worker: with no
// inline comments, the body is the only channel for the findings.
const maxLocalReviewBody = 6000

// localChangesRequestedMessage wakes the worker of a branch with no PR. The
// body carries the findings, since no comment rows exist for a local review.
func localChangesRequestedMessage(run domain.ReviewRun) string {
	var b strings.Builder
	b.WriteString(failureSubject(run, 0))
	b.WriteString(" requested changes.\n")
	if body := strings.TrimSpace(run.Body); body != "" {
		b.WriteString("Review:\n")
		b.WriteString(truncateNotice(domain.SanitizeControlChars(body), maxLocalReviewBody))
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Fix each finding, commit on your branch (do not push), and report ready again. `%s review ls` shows every pass.", brand.CLIName)
	return b.String()
}
