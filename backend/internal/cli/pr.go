package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

type resolveCommentsRequest struct {
	CommentIDs []string `json:"commentIds,omitempty"`
}

func newPRCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Manage pull requests",
	}
	cmd.AddCommand(newPRMergeCommand(ctx))
	cmd.AddCommand(newPRLandCommand(ctx))
	cmd.AddCommand(newPRResolveCommentsCommand(ctx))
	return cmd
}

func newPRMergeCommand(ctx *commandContext) *cobra.Command {
	var prURL, expectedSHA string
	cmd := &cobra.Command{
		Use:   "merge <pr-number>",
		Short: "Merge a pull request",
		Long: `Merge a tracked pull request by number.

The daemon only merges the exact head the caller saw. Unless --url and
--expected-sha are given, the CLI looks the PR up among tracked sessions and
pins the merge to the head SHA of the latest Tallpine-approved review of that PR; it
refuses when there is none. Pass --expected-sha to merge a head you reviewed
yourself. If the same number is tracked in several repositories, pass --url to
disambiguate.`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			prNumber, err := normalizePRNumber(args[0])
			if err != nil {
				return err
			}
			req := apitypes.MergePRRequest{PRURL: strings.TrimSpace(prURL), ExpectedHeadSHA: strings.TrimSpace(expectedSHA)}
			if req.PRURL == "" || req.ExpectedHeadSHA == "" {
				if err := ctx.resolveMergeTarget(cmd.Context(), prNumber, &req); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "merging PR #%s at %s\n", prNumber, req.ExpectedHeadSHA); err != nil {
				return err
			}
			var res apitypes.MergePRResponse
			if err := ctx.postJSON(cmd.Context(), "prs/"+url.PathEscape(prNumber)+"/merge", req, &res); err != nil {
				return withMergePreconditionReason(err)
			}
			if method := strings.TrimSpace(res.Method); method != "" {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "merged PR #%d using %s\n", res.PRNumber, method)
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "merged PR #%d\n", res.PRNumber)
			return err
		},
	}
	cmd.Flags().StringVar(&prURL, "url", "", "PR URL (default: looked up from tracked sessions)")
	cmd.Flags().StringVar(&expectedSHA, "expected-sha", "", "head SHA the merge is pinned to (default: head of the latest Tallpine-approved review)")
	return cmd
}

// withMergePreconditionReason appends the daemon's failed-precondition code
// (details.reason, e.g. ci_unknown) to a PR_PRECONDITIONS_UNMET error so the
// user sees which gate refused the merge.
func withMergePreconditionReason(err error) error {
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) || apiErr.ErrorBody.Code != "PR_PRECONDITIONS_UNMET" {
		return err
	}
	reason, _ := apiErr.ErrorBody.Details["reason"].(string)
	if reason == "" {
		return err
	}
	return fmt.Errorf("%w (reason: %s)", err, reason)
}

// resolveMergeTarget fills the missing PR URL and/or expected head SHA of req
// from the daemon's tracked session PRs. The URL comes from the tracked PRs; the
// head SHA is never taken from the daemon's cached PR head (which the caller has
// not necessarily seen) but from the latest Tallpine review that approved the PR, so
// only a reviewed head can be merged without an explicit --expected-sha.
func (c *commandContext) resolveMergeTarget(ctx context.Context, prNumber string, req *apitypes.MergePRRequest) error {
	number, _ := strconv.Atoi(prNumber)
	var list apitypes.ListSessionsResponse
	if err := c.getJSON(ctx, "sessions", &list); err != nil {
		return err
	}
	var urls []string
	sessionsByURL := map[string][]string{}
	for _, sess := range list.Sessions {
		for _, pr := range sess.PRs {
			if pr.Number != number || pr.URL == "" || (req.PRURL != "" && pr.URL != req.PRURL) {
				continue
			}
			if _, ok := sessionsByURL[pr.URL]; !ok {
				urls = append(urls, pr.URL)
			}
			if !slices.Contains(sessionsByURL[pr.URL], string(sess.ID)) {
				sessionsByURL[pr.URL] = append(sessionsByURL[pr.URL], string(sess.ID))
			}
		}
	}
	switch {
	case len(urls) == 0:
		return fmt.Errorf("PR #%d is not tracked by any session; pass --url and --expected-sha to merge it explicitly", number)
	case len(urls) > 1:
		return usageError{fmt.Errorf("PR #%d is tracked in multiple repositories (%s); pass --url", number, strings.Join(urls, ", "))}
	}
	req.PRURL = urls[0]
	if req.ExpectedHeadSHA != "" {
		return nil
	}
	var approved *domain.ReviewRun
	for _, sessionID := range sessionsByURL[req.PRURL] {
		var reviews apitypes.ListReviewsResponse
		if err := c.getJSON(ctx, "sessions/"+url.PathEscape(sessionID)+"/reviews", &reviews); err != nil {
			return err
		}
		for _, review := range reviews.Reviews {
			if review.PRURL != req.PRURL {
				continue
			}
			if run := approvedReviewRun(review); run != nil && (approved == nil || run.CreatedAt.After(approved.CreatedAt)) {
				approved = run
			}
		}
	}
	if approved == nil {
		return fmt.Errorf("PR #%d has no Tallpine-approved review to pin the merge to; get it reviewed (`tallpine review trigger`) or pass --expected-sha with the head you reviewed", number)
	}
	req.ExpectedHeadSHA = approved.TargetSHA
	return nil
}

// approvedReviewRun returns the run whose approval applies to review: the
// latest run if it approved, else the previous one while a newer pass is still
// running. A latest run that finished with any other verdict supersedes older
// approvals. The returned run always names the head it reviewed.
func approvedReviewRun(review domain.PRReviewState) *domain.ReviewRun {
	for _, run := range []*domain.ReviewRun{review.LatestRun, review.PreviousRun} {
		if run == nil {
			continue
		}
		verdict := strings.TrimSpace(string(run.Verdict))
		sha := strings.TrimSpace(run.TargetSHA)
		if verdict == "approved" && sha != "" {
			approved := *run
			approved.TargetSHA = sha
			return &approved
		}
		if verdict != "" {
			return nil
		}
	}
	return nil
}

func newPRResolveCommentsCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "resolve-comments <pr-number> [comment-id...]",
		Short: "Resolve review threads on a pull request (by comment or thread id)",
		Args:  usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			prNumber, err := normalizePRNumber(args[0])
			if err != nil {
				return err
			}
			commentIDs := make([]string, 0, len(args)-1)
			for _, id := range args[1:] {
				id = strings.TrimSpace(id)
				if id == "" {
					return usageError{errors.New("comment id must not be blank")}
				}
				commentIDs = append(commentIDs, id)
			}
			var res apitypes.ResolveCommentsResponse
			if err := ctx.postJSON(
				cmd.Context(),
				"prs/"+url.PathEscape(prNumber)+"/resolve-comments",
				resolveCommentsRequest{CommentIDs: commentIDs},
				&res,
			); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "resolved %d review thread(s) on PR #%s\n", res.Resolved, prNumber)
			return err
		},
	}
}

func normalizePRNumber(raw string) (string, error) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "#")
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return "", usageError{errors.New("PR number must be a positive integer")}
	}
	return strconv.Itoa(n), nil
}
