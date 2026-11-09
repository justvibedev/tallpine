package review

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
)

// DefaultSweepInterval is how often the review sweep re-checks running passes
// and announces failed passes and approved heads.
const DefaultSweepInterval = time.Minute

// endedRunNoticeBatch bounds one read of ended passes awaiting notice.
const endedRunNoticeBatch = 50

// endedRunNoticeRetryWindow is how long after a pass started Tallpine keeps retrying
// a failure report it cannot file, so one that never succeeds cannot sit at
// the head of the queue forever. The worker is still nudged when Tallpine gives up.
const endedRunNoticeRetryWindow = 24 * time.Hour

// sweepReconcileTimeout bounds one worker's reconcile in the sweep, so a
// startup Chat recovery that never finishes cannot stall the sweep.
const sweepReconcileTimeout = 30 * time.Second

// FailureNoticeStore is the review_run surface the outcome notice sweep needs.
type FailureNoticeStore interface {
	ListSessionsWithRunningReviewRuns(ctx context.Context) ([]domain.SessionID, error)
	ListUnnotifiedEndedReviewRuns(ctx context.Context, limit int) ([]domain.ReviewRun, error)
	MarkReviewRunFailureNotified(ctx context.Context, id string, at time.Time) (bool, error)
	ListReviewRunsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error)
}

// FailureReports files the orchestrator-facing notice as a report on the
// worker's behalf; the report coordinator delivers it like any other.
type FailureReports interface {
	Create(ctx context.Context, input reportsvc.CreateInput) (domain.ReportRecord, error)
}

// WorkerNudger writes a Tallpine-initiated message to the worker's live agent. It
// refuses while the worker waits on its user.
type WorkerNudger interface {
	Nudge(ctx context.Context, id domain.SessionID, message string) (bool, error)
}

type failureNotice struct {
	store   FailureNoticeStore
	reports FailureReports
	nudger  WorkerNudger
}

// SetFailureNotice wires the sweep's store and the two delivery paths. The
// report service is built after the review service, so this is a setter.
func (s *Service) SetFailureNotice(store FailureNoticeStore, reports FailureReports, nudger WorkerNudger) {
	s.noticeMu.Lock()
	defer s.noticeMu.Unlock()
	s.notice = &failureNotice{store: store, reports: reports, nudger: nudger}
}

func (s *Service) failureNotice() *failureNotice {
	s.noticeMu.Lock()
	defer s.noticeMu.Unlock()
	return s.notice
}

// wakeSweep asks a running sweep to run now, so a pass a list or trigger just
// settled is announced without waiting for the next tick.
func (s *Service) wakeSweep() {
	select {
	case s.sweepWake <- struct{}{}:
	default:
	}
}

// StartSweep runs Sweep every interval, and when woken, until ctx is
// cancelled. The returned channel closes when it stops.
func (s *Service) StartSweep(ctx context.Context, interval time.Duration) <-chan struct{} {
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	done := make(chan struct{})
	s.wakeSweep() // Recover unannounced outcomes after a daemon restart.
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.sweepWake:
			}
			if err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
				slog.Default().WarnContext(ctx, "review sweep failed", "err", err)
			}
		}
	}()
	return done
}

// Sweep settles every worker that has a running pass even when nobody lists
// reviews, then announces passes that ended without a verdict. The pass
// deadline runs first and needs neither the worker lock nor Chat recovery, so
// a hung launch or restore cannot keep a pass alive past it. The reconcile
// (exited reviewers, ended turns) then skips a busy worker and gives up on one
// that takes too long, so one stuck worker never stalls the sweep.
func (s *Service) Sweep(ctx context.Context) error {
	if _, err := s.engine.RetireIdleReviewers(ctx); err != nil && ctx.Err() == nil {
		slog.Default().WarnContext(ctx, "review sweep could not retire idle reviewers", "err", err)
	}
	notice := s.failureNotice()
	if notice == nil {
		s.drainQueuedTriggers(ctx)
		return nil
	}
	ids, err := notice.store.ListSessionsWithRunningReviewRuns(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := s.engine.FailOverdueRuns(ctx, id); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Default().WarnContext(ctx, "review sweep could not apply the pass deadline", "session", id, "err", err)
		}
	}
	for _, id := range ids {
		reconcileCtx, cancel := context.WithTimeout(ctx, sweepReconcileTimeout)
		_, err := s.engine.Reconcile(reconcileCtx, id)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Default().DebugContext(ctx, "review sweep could not reconcile session", "session", id, "err", err)
		}
	}
	// After the deadline and reconcile passes, so a hung launch here cannot
	// hold them up and a pass they just freed can start in the same sweep.
	s.drainQueuedTriggers(ctx)
	return s.NotifyEndedRuns(ctx)
}

// NotifyEndedRuns tells the worker and its orchestrator about each pass that
// failed, that Tallpine cancelled without a verdict, or that approved a head. The orchestrator's report
// is filed first under an ID derived from the run, so a retry never files it
// twice. Only then is the run claimed durably, and only the caller that
// claims it nudges the worker. A report that cannot be filed leaves the run
// unclaimed for the next sweep, so a store error or a shutdown between steps
// does not lose the notice. Passes that need no notice are claimed straight
// away so they are not read again.
func (s *Service) NotifyEndedRuns(ctx context.Context) error {
	notice := s.failureNotice()
	if notice == nil {
		return nil
	}
	for {
		runs, err := notice.store.ListUnnotifiedEndedReviewRuns(ctx, endedRunNoticeBatch)
		if err != nil {
			return err
		}
		progressed := false
		for _, run := range runs {
			claimed, err := s.noticeEndedRun(ctx, notice, run)
			if err != nil {
				return err
			}
			progressed = progressed || claimed
		}
		// A batch that claimed nothing is all retries; leave them for the next sweep.
		if len(runs) < endedRunNoticeBatch || !progressed {
			return nil
		}
	}
}

// noticeEndedRun handles one ended pass and reports whether it was claimed.
func (s *Service) noticeEndedRun(ctx context.Context, notice *failureNotice, run domain.ReviewRun) (bool, error) {
	if (run.Status == domain.ReviewRunComplete || run.Status == domain.ReviewRunDelivered) && run.Verdict == domain.VerdictChangesRequested {
		return s.noticeChangesRequested(ctx, notice, run)
	}
	if until, paused := reviewcore.UsageLimitPausedUntil(run); paused && s.clock().Before(until) {
		// The reviewer's quota is out; neither retry nor report until it resets.
		return false, nil
	}
	if s.retryUnsubmittedRun(ctx, notice, run) {
		// The retry is the news; the failure is announced only if it fails too.
		return notice.store.MarkReviewRunFailureNotified(ctx, run.ID, s.clock())
	}
	var nudge func()
	if (run.Status == domain.ReviewRunComplete || run.Status == domain.ReviewRunDelivered) && run.Verdict == domain.VerdictApproved {
		if err := s.fileApprovalReport(ctx, notice, run); err != nil {
			return false, err
		}
	}
	if reviewcore.EndedRunNeedsNotice(run) {
		var filed bool
		nudge, filed = s.fileFailureReport(ctx, notice, run)
		if !filed {
			if s.clock().Sub(run.CreatedAt) < endedRunNoticeRetryWindow {
				return false, nil
			}
			slog.Default().WarnContext(ctx, "giving up on the review failure report", "session", run.SessionID, "run", run.ID)
		}
	}
	claimed, err := notice.store.MarkReviewRunFailureNotified(ctx, run.ID, s.clock())
	if err != nil || !claimed {
		return claimed, err
	}
	if nudge != nil {
		nudge()
	}
	return true, nil
}

// Approval has no inline feedback for SCM to forward. File one durable report
// for the orchestrator; never nudge the writer with a duplicate review message.
func (s *Service) fileApprovalReport(ctx context.Context, notice *failureNotice, run domain.ReviewRun) error {
	if notice.reports == nil {
		return errors.New("review approval report service is not configured")
	}
	worker, found, err := s.store.GetSession(ctx, run.SessionID)
	if err != nil {
		return err
	}
	if !found || worker.Kind != domain.KindWorker || worker.IsTerminated {
		return nil
	}
	message := fmt.Sprintf("[auto] review_approved: %s approved this head. This is an internal %s review, not an independent GitHub approval or permission to merge.", failureSubject(run, 0), brand.ProductName)
	if prs, listErr := s.sessionPRs(ctx, worker); listErr == nil {
		if note := headMovedNote(prs, run); note != "" {
			message += " " + note
		}
	}
	_, err = notice.reports.Create(ctx, reportsvc.CreateInput{
		ID: domain.ReviewApprovedReportIDPrefix + run.ID, SessionID: worker.ID,
		Message: truncateNotice(message, domain.MaxReportTextCharacters), Synthesized: true,
		// A first approval is the merge gate and wakes at once; a repeat for a
		// head already reported is only an FYI.
		Quiet: approvalAlreadyReported(ctx, notice.store, run),
	})
	if errors.Is(err, domain.ErrDuplicateReport) {
		return nil
	}
	return err
}

// failureReportID is the stable report ID for one run's failure notice.
func failureReportID(runID string) string { return "rpt_review_failed_" + runID }

// fileFailureReport files the orchestrator's report for an ended pass and
// returns the worker nudge to send once the run is claimed. filed is false
// when the report could not be filed and the notice should be retried.
func (s *Service) fileFailureReport(ctx context.Context, notice *failureNotice, run domain.ReviewRun) (nudge func(), filed bool) {
	worker, ok, err := s.store.GetSession(ctx, run.SessionID)
	if err != nil {
		slog.Default().WarnContext(ctx, "review failure notice: session lookup failed", "session", run.SessionID, "run", run.ID, "err", err)
		return nil, false
	}
	if !ok || worker.Kind != domain.KindWorker || worker.IsTerminated {
		// A worker that is gone has nobody to tell and no orchestrator waiting on it.
		return nil, true
	}
	prNumber := 0
	if prs, err := s.store.ListPRsBySession(ctx, run.SessionID); err == nil {
		for _, pr := range prs {
			if pr.URL == run.PRURL || pr.HTMLURL == run.PRURL {
				prNumber = pr.Number
				break
			}
		}
	}
	subject := failureSubject(run, prNumber)
	reason := failureReason(run)
	if notice.nudger != nil {
		message := fmt.Sprintf("%s %s without a verdict: %s. No result from it is coming for this commit. `%s review ls` shows every pass; follow your review instructions before requesting another one.",
			subject, failureVerb(run), reason, brand.CLIName)
		nudge = func() {
			if _, err := notice.nudger.Nudge(ctx, worker.ID, message); err != nil {
				slog.Default().WarnContext(ctx, "review failure nudge failed", "session", worker.ID, "run", run.ID, "err", err)
			}
		}
	}
	if notice.reports == nil {
		return nudge, true
	}
	message := fmt.Sprintf("[auto] review_failed: %s %s: %s. No verdict was recorded for this head; `%s review ls %s` shows every pass.",
		subject, failureVerb(run), reason, brand.CLIName, worker.ID)
	_, err = notice.reports.Create(ctx, reportsvc.CreateInput{
		ID: failureReportID(run.ID), SessionID: worker.ID, Message: truncateNotice(message, domain.MaxReportTextCharacters),
		Immediate: true, Synthesized: true,
	})
	if err != nil && !errors.Is(err, domain.ErrDuplicateReport) {
		slog.Default().WarnContext(ctx, "review failure report failed; will retry", "session", worker.ID, "run", run.ID, "err", err)
		return nudge, false
	}
	return nudge, true
}

// failureSubject names the pass: "Tallpine review of PR #12 (head abc123) by codex (gpt-5)".
// headMovedNote says so when a verdict landed for a head the PR has moved past,
// so a stale verdict is never read as covering the current head.
func headMovedNote(prs []domain.PullRequest, run domain.ReviewRun) string {
	for _, pr := range prs {
		if pr.URL != run.PRURL && pr.HTMLURL != run.PRURL {
			continue
		}
		if pr.HeadSHA == "" || run.TargetSHA == "" || pr.HeadSHA == run.TargetSHA {
			return ""
		}
		return fmt.Sprintf("Reviewed %s, PR is now at %s; re-review needed.", shortSHA(run.TargetSHA), shortSHA(pr.HeadSHA))
	}
	return ""
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// approvalAlreadyReported is true when another approved pass on the same PR
// head already filed its notice, so this one tells the orchestrator nothing new.
func approvalAlreadyReported(ctx context.Context, store FailureNoticeStore, run domain.ReviewRun) bool {
	lister, ok := store.(interface {
		ListReviewRunsBySession(context.Context, domain.SessionID) ([]domain.ReviewRun, error)
	})
	if !ok {
		return false
	}
	runs, err := lister.ListReviewRunsBySession(ctx, run.SessionID)
	if err != nil {
		return false
	}
	for _, other := range runs {
		if other.ID != run.ID && other.PRURL == run.PRURL && other.TargetSHA == run.TargetSHA && other.Verdict == domain.VerdictApproved &&
			(other.CreatedAt.Before(run.CreatedAt) || (other.CreatedAt.Equal(run.CreatedAt) && other.ID < run.ID)) {
			return true
		}
	}
	return false
}
