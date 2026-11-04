package cli

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

// Exit codes of `review wait`, so an orchestrator script can branch on the
// outcome without parsing output. 0 is approved.
const (
	exitReviewChangesRequested = 10
	exitReviewNoVerdict        = 11 // the pass failed or was cancelled
	exitReviewTimeout          = 12
	exitReviewNoRun            = 13 // no open PR has a pass on its current head
)

// reviewWaitPollInterval is how often `review wait` re-reads the reviews.
var reviewWaitPollInterval = 2 * time.Second

// reviewWaitNoRunGrace is how long a PR may sit with no pass on its current
// head before `review wait` gives up (a fresh push or trigger takes a moment
// to record its run).
var reviewWaitNoRunGrace = 20 * time.Second

type reviewWaitOptions struct {
	session string
	pr      int
	timeout time.Duration
}

func newReviewWaitCommand(ctx *commandContext) *cobra.Command {
	var opts reviewWaitOptions
	cmd := &cobra.Command{
		Use:   "wait [worker-session-id]",
		Short: "Block until a worker's review reaches a verdict",
		Long: "Block until every review pass on the worker's PR head has ended, then print the verdict.\n" +
			"Exit status: 0 approved, 10 changes requested, 11 failed or cancelled without a verdict, 12 timed out, 13 no review run for the open PR's current head.\n" +
			"Waits on the session's open PRs only; --pr N picks one. Prints one line per PR: verdict, PR number, head SHA.",
		Args: atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.waitReview(cmd, args, opts)
		},
	}
	cmd.Flags().StringVar(&opts.session, "session", "", "Worker session id (or pass it as the positional argument)")
	cmd.Flags().IntVar(&opts.pr, "pr", 0, "Wait on this PR number only")
	cmd.Flags().DurationVar(&opts.timeout, "timeout", 30*time.Minute, "Give up after this long (exit status 12)")
	return cmd
}

// prWaitResult is one open PR's settled verdict on its current head.
type prWaitResult struct {
	PR      int
	SHA     string
	Verdict string
}

// reviewWaitOutcome inspects the open, review-eligible PRs (merged, closed and
// otherwise ineligible ones never count; pr != 0 narrows to that one). It
// returns each settled PR's verdict on its current head, and whether the wait
// must go on: running is true while any pass on a current head runs, missing
// is true while an eligible PR has no pass at all on its current head. A
// verdict recorded against an older head never counts.
func reviewWaitOutcome(reviews []domain.PRReviewState, pr int) (results []prWaitResult, running, missing bool) {
	for _, review := range reviews {
		if pr != 0 && review.PRNumber != pr {
			continue
		}
		if review.Status == "ineligible" {
			continue
		}
		var runs []domain.ReviewRun
		for _, run := range headRuns(review) {
			if review.TargetSHA == "" || run.TargetSHA == "" || run.TargetSHA == review.TargetSHA {
				runs = append(runs, run)
			}
		}
		if len(runs) == 0 {
			missing = true
			continue
		}
		review.CurrentHeadRuns, review.LatestRun = runs, nil
		for _, run := range runs {
			if run.Status == "running" {
				running = true
			}
		}
		verdict := reviewHeadVerdict(review)
		switch verdict {
		case "changes_requested", "approved":
		default:
			verdict = "no_verdict"
		}
		results = append(results, prWaitResult{PR: review.PRNumber, SHA: review.TargetSHA, Verdict: verdict})
	}
	return results, running, missing
}

// aggregateVerdict: changes requested beats a failed pass beats approval.
func aggregateVerdict(results []prWaitResult) string {
	out := "approved"
	for _, r := range results {
		switch r.Verdict {
		case "changes_requested":
			return r.Verdict
		case "no_verdict":
			out = r.Verdict
		}
	}
	return out
}

func (c *commandContext) waitReview(cmd *cobra.Command, args []string, opts reviewWaitOptions) error {
	session := reviewTargetSession(args, opts.session)
	if session == "" {
		return usageError{errors.New("usage: worker session id is required (positional or --session) outside a Tallpine session")}
	}
	path := "sessions/" + url.PathEscape(session) + "/reviews"
	deadline := time.Now().Add(opts.timeout)
	var missingSince time.Time
	for {
		var res apitypes.ListReviewsResponse
		if err := c.getJSON(cmd.Context(), path, &res); err != nil {
			return err
		}
		results, running, missing := reviewWaitOutcome(res.Reviews, opts.pr)
		if !missing {
			missingSince = time.Time{}
		} else if missingSince.IsZero() {
			missingSince = time.Now()
		}
		switch {
		case running:
		case missing && time.Since(missingSince) >= reviewWaitNoRunGrace:
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "no_run"); err != nil {
				return err
			}
			return exitCodeError{code: exitReviewNoRun}
		case missing:
		case len(results) == 0:
			// No open review-eligible PR at all: nothing will ever settle.
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "no_run"); err != nil {
				return err
			}
			return exitCodeError{code: exitReviewNoRun}
		default:
			for _, r := range results {
				sha := r.SHA
				if sha == "" {
					sha = "-"
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s pr=%d head=%s\n", r.Verdict, r.PR, sha); err != nil {
					return err
				}
			}
			switch aggregateVerdict(results) {
			case "approved":
				return nil
			case "changes_requested":
				return exitCodeError{code: exitReviewChangesRequested}
			}
			return exitCodeError{code: exitReviewNoVerdict}
		}
		if !time.Now().Add(reviewWaitPollInterval).Before(deadline) {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "timeout\n"); err != nil {
				return err
			}
			return exitCodeError{code: exitReviewTimeout}
		}
		select {
		case <-cmd.Context().Done():
			return cmd.Context().Err()
		case <-time.After(reviewWaitPollInterval):
		}
	}
}
