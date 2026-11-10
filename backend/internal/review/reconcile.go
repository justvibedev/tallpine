package review

import (
	stdctx "context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// reconcileExitedReviewer converts a definitively dead reviewer, a live
// terminal reviewer whose turn ended without submitting, or a pass past its
// deadline into durable failed-run facts. Probe errors are intentionally
// ignored: an unknown runtime state is not proof that the reviewer exited.
func (e *Engine) reconcileExitedReviewer(ctx stdctx.Context, review *domain.Review, runs []domain.ReviewRun) (bool, error) {
	if review.ID == "" {
		return false, nil
	}
	running := runningRunsForReview(runs, review.ID)
	if len(running) == 0 {
		return false, nil
	}
	// The deadline needs no runtime facts: a reviewer wedged mid-turn reads
	// active and alive forever.
	overdue, running, err := e.failOverdueReviewerRuns(ctx, running)
	if err != nil {
		return false, err
	}
	// Any state can hide a dead pane: a fresh launch has no hook state yet, and
	// a finished turn reads idle.
	if len(running) == 0 || review.ReviewerHandleID == "" {
		return overdue, nil
	}
	// The session-end hook already proved this launch exited; the hook only
	// records the state, so its passes are settled here.
	alive := false
	if review.ReviewerActivityState != domain.ActivityExited {
		var err error
		alive, err = e.launcher.Alive(ctx, review.ReviewerHandleID, review.ReviewerLaunchID)
		if err != nil {
			// An unavailable probe is unknown, not evidence that the reviewer died.
			alive = true
		}
	}
	if alive {
		unstarted, remaining, err := e.failUnstartedReviewerRuns(ctx, review, running)
		if err != nil {
			return false, err
		}
		idle, err := e.failIdleReviewerRuns(ctx, *review, remaining)
		return overdue || unstarted || idle, err
	}
	for _, run := range running {
		if _, err := e.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunFailed, domain.VerdictNone, reviewerExitedBeforeSubmission, "", run.AutoInjectReview); err != nil {
			return false, err
		}
	}
	review.ReviewerActivityState = domain.ActivityExited
	review.UpdatedAt = e.clock()
	if err := e.store.UpsertReview(ctx, *review); err != nil {
		return false, err
	}
	return true, nil
}

func runningRunsForReview(runs []domain.ReviewRun, reviewID string) []domain.ReviewRun {
	var running []domain.ReviewRun
	for _, run := range runs {
		if run.ReviewID == reviewID && run.Status == domain.ReviewRunRunning {
			running = append(running, run)
		}
	}
	return running
}

// failUnsubmittedReviewerRuns fails a live reviewer's passes that are past
// the pass deadline or whose turn ended without a submit.
func (e *Engine) failUnsubmittedReviewerRuns(ctx stdctx.Context, review domain.Review, running []domain.ReviewRun) (bool, error) {
	overdue, remaining, err := e.failOverdueReviewerRuns(ctx, running)
	if err != nil {
		return false, err
	}
	idle, err := e.failIdleReviewerRuns(ctx, review, remaining)
	return overdue || idle, err
}

// failOverdueReviewerRuns fails running passes older than the pass deadline
// and returns the passes still within it. A healthy review of a large PR
// finishes well inside the deadline; one past it is wedged or lost, and would
// otherwise hold its reviewer's slot on the head forever.
func (e *Engine) failOverdueReviewerRuns(ctx stdctx.Context, running []domain.ReviewRun) (bool, []domain.ReviewRun, error) {
	if e.passDeadline <= 0 {
		return false, running, nil
	}
	now := e.clock()
	changed := false
	remaining := make([]domain.ReviewRun, 0, len(running))
	for _, run := range running {
		if run.CreatedAt.IsZero() || now.Sub(run.CreatedAt) < e.passDeadline {
			remaining = append(remaining, run)
			continue
		}
		updated, err := e.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunFailed, domain.VerdictNone, reviewerPassDeadlineReason(e.passDeadline), "", run.AutoInjectReview)
		if err != nil {
			return false, nil, err
		}
		changed = changed || updated
	}
	return changed, remaining, nil
}

func reviewerPassDeadlineReason(deadline time.Duration) string {
	return fmt.Sprintf("reviewer did not submit a result within the %s review deadline", deadline)
}

// ScreenReader is an optional Launcher capability: the visible tail of a
// reviewer terminal, for the failure report of a reviewer that never started.
type ScreenReader interface {
	Screen(ctx stdctx.Context, handleID string, lines int) (string, error)
}

// ReviewerReminder is an optional Launcher capability: type a message into a
// live terminal reviewer, to wake one whose turn ended without a submit.
type ReviewerReminder interface {
	Remind(ctx stdctx.Context, handleID, message string) error
}

// reviewerReminderGrace is how long a reminded reviewer may stay idle again
// before its pass is failed.
const reviewerReminderGrace = 3 * time.Minute

const reviewerSubmitReminder = "Your turn ended but no review result was recorded. Do not wait on any background command. Finish now: post the GitHub review (step 1 of the review task) and then run the `tallpine review submit` command (step 2). If you are blocked, submit what you have."

func (e *Engine) reminderSentAt(runID string) (time.Time, bool) {
	e.launchedMu.Lock()
	defer e.launchedMu.Unlock()
	at, ok := e.remindedAt[runID]
	return at, ok
}

func (e *Engine) forgetReminded(runID string) {
	e.launchedMu.Lock()
	delete(e.remindedAt, runID)
	e.launchedMu.Unlock()
}

func (e *Engine) markReminded(runID string) {
	e.launchedMu.Lock()
	e.remindedAt[runID] = e.clock()
	e.launchedMu.Unlock()
}

// ReviewerNotStartedPrefix starts the body of a pass whose reviewer terminal
// never reported activity (stuck on a startup screen); the rest is the screen.
const ReviewerNotStartedPrefix = "reviewer did not start: "

// reviewerLaunchGrace is how long a Claude terminal reviewer gets to report its
// first hook (SessionStart or prompt submit) after a pass starts. A healthy
// launch reports within seconds; one stuck on a usage-limit, trust or model
// dialog never does, and would otherwise sit running until the pass deadline.
const reviewerLaunchGrace = 2 * time.Minute

const reviewerScreenLines = 40

const reviewerScreenMaxBytes = 2000

// failUnstartedReviewerRuns fails the passes of a live reviewer that never
// began its work, with the reason as the error, and releases the reviewer so
// the retry launches a fresh one. What counts as "began" depends on the surface:
//   - Chat: the pass's prompt reached the provider (the turn is bound to a
//     provider turn id). A turn the provider never accepted stays queued.
//   - Claude terminal: an activity hook stamped the review row after the pass
//     began. Claude is the only terminal reviewer that reports hooks.
//   - Other terminal reviewers have no hooks: a pane that is still blank at the
//     grace never started its CLI.
func (e *Engine) failUnstartedReviewerRuns(ctx stdctx.Context, review *domain.Review, running []domain.ReviewRun) (bool, []domain.ReviewRun, error) {
	if e.launchGrace <= 0 || review.ReviewerHandleID == "" {
		return false, running, nil
	}
	chat := review.InterfaceMode == domain.ReviewerInterfaceChat
	now := e.clock()
	screen, screenOK, screenRead := "", false, false
	var stuck, remaining []domain.ReviewRun
	var reason string
	usageLimited := false
	for _, run := range running {
		if run.CreatedAt.IsZero() || now.Sub(e.graceStart(review.ReviewerHandleID, run.CreatedAt)) < e.launchGrace {
			remaining = append(remaining, run)
			continue
		}
		var runReason string
		switch {
		case chat:
			if run.BatchID != "" {
				dispatched, err := e.store.ReviewBatchDispatched(ctx, review.ID, run.BatchID)
				if err != nil {
					return false, nil, err
				}
				if !dispatched {
					runReason = "the review prompt never reached the provider"
				}
			}
		case review.Harness == domain.ReviewerClaudeCode:
			// A hook bumps the row's updated_at past the pass's start (UpsertReview
			// never lowers it, so one that landed mid-launch survives). The launch
			// writes the pass's own start time, and hooks are stamped to the second,
			// so only a stamp more than a second later is proof of activity.
			if !review.UpdatedAt.IsZero() && !review.UpdatedAt.After(run.CreatedAt.Add(time.Second)) {
				if !screenRead {
					screen, screenOK = e.readReviewerScreen(ctx, review.ReviewerHandleID)
					screenRead = true
				}
				message, limited := usageLimitOnScreen(screen, now)
				switch {
				case limited:
					// A pane stuck on the quota refusal is waiting for the reset,
					// not broken: report it as a usage-limit wait with the reset.
					usageLimited = true
					runReason = ReviewerUsageLimitPrefix + message
				case screen != "":
					runReason = screen
				case screenOK:
					runReason = "no hook activity and the terminal screen was blank"
				default:
					runReason = "no hook activity and the terminal screen could not be read"
				}
				if !limited && strings.Contains(strings.ToLower(screen), "trusted folder") {
					runReason = "Claude is waiting on its " + reviewerTrustDialogMarker + " (the launch config has no trust entry for this worktree): " + screen
				}
			}
		default:
			if !screenRead {
				screen, screenOK = e.readReviewerScreen(ctx, review.ReviewerHandleID)
				screenRead = true
			}
			// An unreadable pane is unknown, not stuck; only a readable blank one is.
			if screenOK && screen == "" {
				runReason = "the terminal showed no output"
			}
		}
		if runReason == "" {
			remaining = append(remaining, run)
			continue
		}
		reason = runReason
		stuck = append(stuck, run)
	}
	if len(stuck) == 0 {
		return false, running, nil
	}
	body := ReviewerNotStartedPrefix + reason
	if usageLimited {
		body = reason
	}
	changed := false
	for _, run := range stuck {
		updated, err := e.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunFailed, domain.VerdictNone, body, "", run.AutoInjectReview)
		if err != nil {
			return false, nil, err
		}
		changed = changed || updated
	}
	if changed {
		// The reviewer is wedged: a retry must not be handed to it.
		if err := e.launcher.Destroy(ctx, review.ReviewerHandleID); err != nil {
			slog.Warn("review: could not release a reviewer that never started", "review", review.ID, "err", err)
		}
		if err := e.store.ClearReviewerHandleByHarness(ctx, review.SessionID, review.Harness); err != nil {
			return false, nil, err
		}
		review.ReviewerHandleID = ""
	}
	return changed, remaining, nil
}

func (e *Engine) markLaunched(handleID string) {
	if handleID == "" {
		return
	}
	e.launchedMu.Lock()
	e.launchedAt[handleID] = e.clock()
	e.launchedMu.Unlock()
}

// graceStart is when the launch grace began: the pass's creation, or later if
// the reviewer pane finished launching after it.
func (e *Engine) graceStart(handleID string, created time.Time) time.Time {
	e.launchedMu.Lock()
	defer e.launchedMu.Unlock()
	if at, ok := e.launchedAt[handleID]; ok && at.After(created) {
		return at
	}
	return created
}

// readReviewerScreen returns the trimmed visible tail of a reviewer terminal.
// ok is false when the launcher cannot read it or the read failed: an unknown
// pane, which is never evidence that the reviewer is stuck.
func (e *Engine) readReviewerScreen(ctx stdctx.Context, handleID string) (string, bool) {
	reader, ok := e.launcher.(ScreenReader)
	if !ok {
		return "", false
	}
	out, err := reader.Screen(ctx, handleID, reviewerScreenLines)
	if err != nil {
		return "", false
	}
	return tailScreen(out, reviewerScreenMaxBytes), true
}

// tailScreen keeps the last limit bytes of a terminal capture, without blank lines.
func tailScreen(out string, limit int) string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if t := strings.TrimRight(line, " \t\r"); strings.TrimSpace(t) != "" {
			lines = append(lines, t)
		}
	}
	text := strings.Join(lines, "\n")
	if len(text) > limit {
		text = strings.ToValidUTF8(text[len(text)-limit:], "")
	}
	return text
}

// failIdleReviewerRuns fails the running passes of a live terminal reviewer
// that has sat idle at its prompt for the idle grace: its turn ended without
// a submit and nothing will wake it. Chat reviewers settle the same case when
// their turn is projected, so they are left alone. The pane stays reusable.
func (e *Engine) failIdleReviewerRuns(ctx stdctx.Context, review domain.Review, running []domain.ReviewRun) (bool, error) {
	if e.idleGrace <= 0 || review.InterfaceMode == domain.ReviewerInterfaceChat || review.ReviewerActivityState != domain.ActivityIdle || review.UpdatedAt.IsZero() {
		return false, nil
	}
	now := e.clock()
	apiGrace := min(e.idleGrace, reviewerAPIErrorGrace)
	idleFor := now.Sub(review.UpdatedAt)
	if idleFor < apiGrace {
		return false, nil
	}
	changed := false
	body := reviewerIdleWithoutSubmission
	limited, apiErr := false, false
	grace := e.idleGrace
	if screen, ok := e.readReviewerScreen(ctx, review.ReviewerHandleID); ok {
		if message, ok := usageLimitOnScreen(screen, now); ok {
			body, limited = ReviewerUsageLimitPrefix+message, true
		} else if message, ok := apiErrorOnScreen(screen); ok {
			body, apiErr, grace = ReviewerAPIErrorPrefix+message, true, apiGrace
		}
	}
	reminder, canRemind := e.launcher.(ReviewerReminder)
	canRemind = canRemind && !limited && !apiErr
	remindGrace := min(e.idleGrace, reviewerReminderGrace)
	remindTried, remindSent := false, false
	for _, run := range running {
		if run.CreatedAt.IsZero() || now.Sub(run.CreatedAt) < grace {
			continue
		}
		if canRemind {
			// B651: a reviewer can end its turn while parked on a background
			// command and never wake. Ask it to finish before the pass counts as
			// failed; only a pane still idle after the reminder fails the pass.
			if at, sent := e.reminderSentAt(run.ID); sent {
				if now.Sub(at) < remindGrace || idleFor < remindGrace {
					continue
				}
			} else if idleFor < e.idleGrace {
				continue
			} else {
				// One pane can hold several running runs (a batch): wake it once.
				if !remindTried {
					remindTried = true
					if err := reminder.Remind(ctx, review.ReviewerHandleID, reviewerSubmitReminder); err != nil {
						slog.Warn("review: could not remind an idle reviewer to submit", "review", review.ID, "err", err)
					} else {
						remindSent = true
					}
				}
				if remindSent {
					e.markReminded(run.ID)
					continue
				}
			}
		} else if idleFor < e.idleGrace && !apiErr {
			continue
		}
		updated, err := e.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunFailed, domain.VerdictNone, body, "", run.AutoInjectReview)
		if err != nil {
			return false, err
		}
		e.forgetReminded(run.ID)
		changed = changed || updated
	}
	if changed && limited {
		// Claude resumes a limited pane on its own after the reset. The sweep
		// retries the pass then, so exactly one reviewer may act on the head: stop
		// this pane (and forget its handle) before the retry starts another.
		if err := e.launcher.Destroy(ctx, review.ReviewerHandleID); err != nil {
			slog.Warn("review: could not stop a usage-limited reviewer", "review", review.ID, "err", err)
		}
		if err := e.store.ClearReviewerHandleByHarness(ctx, review.SessionID, review.Harness); err != nil {
			return false, err
		}
	}
	return changed, nil
}

func reviewerSurface(review domain.Review) domain.ReviewerSurface {
	if review.ID == "" || review.IsArchived {
		return domain.ReviewerSurface{}
	}
	handleID := review.ReviewerHandleID
	if review.InterfaceMode == domain.ReviewerInterfaceChat {
		handleID = ""
	}
	return domain.ReviewerSurface{
		Mode: review.InterfaceMode, ReviewID: review.ID, Harness: review.Harness,
		HandleID: handleID, ControllerError: review.ControllerError,
	}
}

func legacyReviewerHandle(review domain.Review) string {
	if review.IsArchived || review.InterfaceMode == domain.ReviewerInterfaceChat {
		return ""
	}
	return review.ReviewerHandleID
}

var screenResetClock = regexp.MustCompile(`(?i)continuing automatically at (\d{1,2})(?::(\d{2}))?\s*(am|pm)`)

// usageLimitOnScreen finds the provider's quota refusal in a reviewer terminal
// capture. A terminal reviewer that hit the limit sits idle at its prompt, so
// without this the pass fails as "ended without submitting" instead of waiting
// for the reset like a Chat reviewer's does. The returned message always carries
// an ISO reset the usage-limit parser accepts: the screen's own, or a fallback
// measured from now (the failure), never from the pass start.
func usageLimitOnScreen(screen string, now time.Time) (string, bool) {
	first := ""
	for _, line := range strings.Split(screen, "\n") {
		message := strings.TrimSpace(strings.TrimLeftFunc(line, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }))
		limit := domain.ParseConversationUsageLimit(message, &now)
		if limit == nil {
			continue
		}
		if limit.ResetsAt != nil {
			return message, true
		}
		if m := screenResetClock.FindStringSubmatch(message); m != nil {
			// Claude prints the clock in the host's local zone, which this pane shares.
			if reset, ok := nextClock(now.In(time.Local), m[1], m[2], m[3]); ok {
				return "Usage limit reached · resets " + reset.UTC().Format(time.RFC3339), true
			}
		}
		if first == "" {
			first = message
		}
	}
	if first == "" {
		return "", false
	}
	return first + " · resets " + now.Add(domain.UsageLimitFallbackPause).UTC().Format(time.RFC3339), true
}

// nextClock is the next occurrence after now of a 12-hour clock time.
func nextClock(now time.Time, hour, minute, meridiem string) (time.Time, bool) {
	h, _ := strconv.Atoi(hour)
	m, _ := strconv.Atoi(minute)
	if h < 1 || h > 12 || m > 59 {
		return time.Time{}, false
	}
	h %= 12
	if strings.EqualFold(meridiem, "pm") {
		h += 12
	}
	reset := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if !reset.After(now) {
		reset = reset.AddDate(0, 0, 1)
	}
	return reset, true
}
