package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

type landPRView struct {
	Number           int    `json:"number"`
	HeadRefOid       string `json:"headRefOid"`
	BaseRefName      string `json:"baseRefName"`
	MergeStateStatus string `json:"mergeStateStatus"`
}

func newPRLandCommand(ctx *commandContext) *cobra.Command {
	var sessionID string
	var killWorker, dryRun, allowOverlap bool
	cmd := &cobra.Command{
		Use:   "land <pr-number>",
		Short: "Run the orchestrator merge checklist and squash-merge a worker's PR",
		Long: `Land a worker's PR in one step (orchestrator only). Run from a checkout of the PR's repository.

1. Finds the head of the latest approved review of the worker's PR.
2. If the PR head differs, accepts it only when the rebased patch equals the
   approved patch (git diff <old-base> <approved-head> vs <new-base> <pr-head>,
   with index lines and hunk line numbers normalized); otherwise refuses.
3. Refuses unless GitHub mergeStateStatus is CLEAN.
4. Merges via the daemon (POST prs/<n>/merge), pinned to the head.
5. --kill-worker terminates the worker session after the merge.

--dry-run prints the decision and changes nothing.`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			prNumber, err := normalizePRNumber(args[0])
			if err != nil {
				return err
			}
			if strings.TrimSpace(sessionID) == "" {
				return usageError{errors.New("--session is required")}
			}
			return ctx.landPR(cmd, prNumber, strings.TrimSpace(sessionID), killWorker, dryRun, allowOverlap)
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "worker session that owns the PR")
	cmd.Flags().BoolVar(&killWorker, "kill-worker", false, "terminate the worker session after a successful merge")
	cmd.Flags().BoolVar(&allowOverlap, "allow-overlap", false, "land a rebased PR even when main changed files the PR also touches")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the decision without merging")
	return cmd
}

func (c *commandContext) landPR(cmd *cobra.Command, prNumber, sessionID string, killWorker, dryRun, allowOverlap bool) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	number := 0
	_, _ = fmt.Sscanf(prNumber, "%d", &number)

	var list apitypes.ListSessionsResponse
	if err := c.getJSON(ctx, "sessions", &list); err != nil {
		return err
	}
	prURL := ""
	found := false
	for _, sess := range list.Sessions {
		if string(sess.ID) != sessionID {
			continue
		}
		found = true
		for _, pr := range sess.PRs {
			if pr.Number == number && pr.URL != "" {
				prURL = pr.URL
			}
		}
	}
	if !found {
		return fmt.Errorf("session %s not found", sessionID)
	}
	if prURL == "" {
		return fmt.Errorf("PR #%d is not tracked by session %s", number, sessionID)
	}

	// (a) latest approved review head for this worker's PR.
	var reviews apitypes.ListReviewsResponse
	if err := c.getJSON(ctx, "sessions/"+url.PathEscape(sessionID)+"/reviews", &reviews); err != nil {
		return err
	}
	var approved *domain.ReviewRun
	for _, review := range reviews.Reviews {
		if review.PRURL != prURL {
			continue
		}
		if run := approvedReviewRun(review); run != nil && (approved == nil || run.CreatedAt.After(approved.CreatedAt)) {
			approved = run
		}
	}
	if approved == nil {
		return fmt.Errorf("refusing to land PR #%d: no approved review", number)
	}

	raw, err := c.deps.CommandOutput(ctx, "gh", "pr", "view", prURL, "--json", "number,headRefOid,baseRefName,mergeStateStatus")
	if err != nil {
		return fmt.Errorf("gh pr view: %w", err)
	}
	var view landPRView
	if err := json.Unmarshal(raw, &view); err != nil {
		return fmt.Errorf("decode gh pr view: %w", err)
	}
	head := strings.TrimSpace(view.HeadRefOid)
	if head == "" {
		return errors.New("gh pr view returned no head commit")
	}

	// (b) head moved since approval: only an identical rebased patch is accepted.
	if head != approved.TargetSHA {
		same, overlap, err := c.landPatchesMatch(ctx, prNumber, view.BaseRefName, approved.TargetSHA, head)
		if err != nil {
			return err
		}
		if !same {
			return fmt.Errorf("refusing to land PR #%d: head %s differs from approved head %s and the patch changed; re-review", number, head, approved.TargetSHA)
		}
		if len(overlap) > 0 && !allowOverlap {
			return fmt.Errorf("refusing to land PR #%d: main changed files the PR also touches since the approved base (%s); the merged result differs from what was reviewed; re-review or pass --allow-overlap", number, strings.Join(overlap, ", "))
		}
		_, _ = fmt.Fprintf(out, "head %s is a rebase of approved head %s (identical patch)\n", head, approved.TargetSHA)
	}

	// (c) merge state.
	if status := strings.TrimSpace(view.MergeStateStatus); status != "CLEAN" {
		return fmt.Errorf("refusing to land PR #%d: mergeStateStatus is %q, want CLEAN", number, status)
	}

	if dryRun {
		_, err := fmt.Fprintf(out, "dry-run: would squash-merge PR #%d at %s via the daemon\n", number, head)
		return err
	}

	// (d) merge through the daemon route so its PR precondition gates, head
	// pinning and tracked-PR refresh apply, exactly as for `tallpine pr merge`.
	var res apitypes.MergePRResponse
	if err := c.postJSON(ctx, "prs/"+url.PathEscape(prNumber)+"/merge", apitypes.MergePRRequest{PRURL: prURL, ExpectedHeadSHA: head}, &res); err != nil {
		return withMergePreconditionReason(err)
	}
	if _, err := fmt.Fprintf(out, "merged PR #%d at %s\n", number, head); err != nil {
		return err
	}

	// (e) optionally terminate the worker.
	if killWorker {
		return c.killSession(ctx, cmd, sessionID, sessionOptions{})
	}
	return nil
}

var (
	landIndexLine = regexp.MustCompile(`(?m)^index [0-9a-f]+\.\.[0-9a-f]+.*\n`)
	landHunkLine  = regexp.MustCompile(`(?m)^@@ -\d+(,\d+)? \+\d+(,\d+)? @@`)
)

func normalizeLandDiff(b []byte) []byte {
	b = landIndexLine.ReplaceAll(b, nil)
	return landHunkLine.ReplaceAll(b, []byte("@@"))
}

// landPatchesMatch fetches both heads and compares git diff <base> <head> for
// each, where base is the merge-base with the PR's base branch. It also returns
// the files changed on the base branch between the two bases that the PR
// touches: for those, identical hunks do not imply an identical merged file.
func (c *commandContext) landPatchesMatch(ctx context.Context, prNumber, baseRef, oldHead, newHead string) (bool, []string, error) {
	git := func(args ...string) ([]byte, error) {
		b, err := c.deps.CommandOutput(ctx, "git", args...)
		if err != nil {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return b, nil
	}
	if _, err := git("fetch", "origin", baseRef, "refs/pull/"+prNumber+"/head"); err != nil {
		return false, nil, err
	}
	// The old head may be unreachable after a force-push; fetch it by SHA.
	if _, err := git("fetch", "origin", oldHead); err != nil {
		return false, nil, err
	}
	var bases [2]string
	var diffs [2][]byte
	for i, head := range []string{oldHead, newHead} {
		base, err := git("merge-base", head, "origin/"+baseRef)
		if err != nil {
			return false, nil, err
		}
		bases[i] = strings.TrimSpace(string(base))
		d, err := git("diff", "--no-color", "--no-ext-diff", bases[i], head)
		if err != nil {
			return false, nil, err
		}
		diffs[i] = normalizeLandDiff(d)
	}
	if len(diffs[0]) == 0 || !bytes.Equal(diffs[0], diffs[1]) {
		return false, nil, nil
	}
	mainFiles, err := git("diff", "--name-only", bases[0], bases[1])
	if err != nil {
		return false, nil, err
	}
	prFiles, err := git("diff", "--name-only", bases[1], newHead)
	if err != nil {
		return false, nil, err
	}
	touched := map[string]bool{}
	for _, f := range strings.Fields(string(prFiles)) {
		touched[f] = true
	}
	var overlap []string
	for _, f := range strings.Fields(string(mainFiles)) {
		if touched[f] {
			overlap = append(overlap, f)
		}
	}
	return true, overlap, nil
}
