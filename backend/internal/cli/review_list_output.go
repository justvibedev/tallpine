package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func writeReviewList(cmd *cobra.Command, session string, res apitypes.ListReviewsResponse) error {
	out := cmd.OutOrStdout()
	for _, q := range res.QueuedReviews {
		if _, err := fmt.Fprintf(out, "queued: %s review of %s waits for its reviewer to be free (attempts %d); `tallpine review cancel %s` withdraws it\n", q.Harness, q.PRURL, q.Attempts, session); err != nil {
			return err
		}
	}
	if len(res.Reviews) == 0 {
		_, err := fmt.Fprintf(out, "No reviews found for %s.\n", session)
		return err
	}

	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PR\tSTATUS\tVERDICT\tTITLE"); err != nil {
		return err
	}
	hasRuns := false
	for _, review := range res.Reviews {
		hasRuns = hasRuns || len(headRuns(review)) > 0
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", reviewTargetLabel(review), review.Status, reviewHeadVerdict(review), review.Title); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, review := range res.Reviews {
		if run := review.OlderHeadRun; run != nil {
			if _, err := fmt.Fprintf(out, "%s: %s still reviewing %s, head moved to %s (run %s)\n", reviewTargetLabel(review), reviewerLabel(*run), shortReviewSHA(run.TargetSHA), shortReviewSHA(review.TargetSHA), run.ID); err != nil {
				return err
			}
		}
	}
	if !hasRuns {
		return nil
	}

	// Every pass on each PR's current head, so a second reviewer's verdict is
	// never hidden behind the first. A failed or cancelled pass plans as
	// needs_review, so its reason is printed too.
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	tw = tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PR\tREVIEWER\tSTATUS\tVERDICT\tSTARTED\tRUN"); err != nil {
		return err
	}
	var reasons []string
	for _, review := range res.Reviews {
		for _, run := range headRuns(review) {
			verdict := run.Verdict
			if verdict == "" {
				verdict = "-"
			}
			started := "-"
			if !run.CreatedAt.IsZero() {
				started = run.CreatedAt.UTC().Format("2006-01-02 15:04 MST")
			}
			if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", reviewTargetLabel(review), reviewerLabel(run), run.Status, verdict, started, run.ID); err != nil {
				return err
			}
			if run.PostFailed {
				reasons = append(reasons, fmt.Sprintf("%s %s: not posted to the PR; the findings exist only in Tallpine", reviewTargetLabel(review), reviewerLabel(run)))
			}
			if run.Status == "failed" || run.Status == "cancelled" {
				if reason := strings.Join(strings.Fields(run.Body), " "); reason != "" {
					reasons = append(reasons, fmt.Sprintf("%s %s %s: %s", reviewTargetLabel(review), reviewerLabel(run), run.Status, reason))
				}
			}
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, reason := range reasons {
		if _, err := fmt.Fprintln(out, reason); err != nil {
			return err
		}
	}
	return nil
}

// headRuns returns the passes on the PR's current head, falling back to the
// latest run for a daemon that predates currentHeadRuns.
func headRuns(review domain.PRReviewState) []domain.ReviewRun {
	if len(review.CurrentHeadRuns) > 0 {
		return review.CurrentHeadRuns
	}
	if review.LatestRun != nil {
		return []domain.ReviewRun{*review.LatestRun}
	}
	return nil
}

// reviewHeadVerdict summarizes the head across reviewers: any reviewer's
// latest request for changes wins over another's approval; with no verdict
// yet, the newest pass's failure or cancellation shows.
func reviewHeadVerdict(review domain.PRReviewState) string {
	runs := headRuns(review)
	seen := make(map[string]bool)
	approved := false
	for _, run := range runs { // newest first
		if run.Verdict != "approved" && run.Verdict != "changes_requested" {
			continue
		}
		key := string(run.Harness) + "\x00" + run.Model
		if seen[key] {
			continue
		}
		seen[key] = true
		if run.Verdict == "changes_requested" {
			return "changes_requested"
		}
		approved = true
	}
	if approved {
		return "approved"
	}
	if len(runs) > 0 && (runs[0].Status == "failed" || runs[0].Status == "cancelled") {
		return string(runs[0].Status)
	}
	return "-"
}

func reviewerLabel(run domain.ReviewRun) string {
	label := string(run.Harness)
	if label == "" {
		label = "reviewer"
	}
	if run.Model != "" {
		label += " (" + run.Model + ")"
	}
	if run.ModelReason != "" {
		label += " [" + run.ModelReason + "]"
	}
	return label
}

// reviewTargetLabel names what a review covers: "#<n>" for a PR, or the
// branch for a session branch reviewed without a PR (no PR number exists).
func reviewTargetLabel(review domain.PRReviewState) string {
	if domain.IsLocalReviewTarget(review.PRURL) {
		return "branch " + strings.TrimPrefix(review.PRURL, domain.LocalReviewTargetPrefix)
	}
	return fmt.Sprintf("#%d", review.PRNumber)
}
