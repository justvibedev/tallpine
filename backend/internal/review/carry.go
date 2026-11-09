package review

import (
	stdctx "context"
	"fmt"
	"log/slog"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/process"
)

// carryApprovals keeps an approval across a push that leaves the patch
// unchanged (a clean rebase). For each open PR whose head has no run from this
// reviewer, it compares the patch-id of the PR's diff at the head with that at
// the head of the reviewer's latest approved run; when equal it records an
// approved run for the new head, so no second review starts. Off unless the
// project sets ApproveIdenticalPatch. Any failure leaves the normal review.
func (p *triggerPass) carryApprovals() (TriggerResult, bool, error) {
	e := p.e
	if e.projects == nil || e.patchID == nil || p.opts.Rerun || p.opts.RejectReviewedHead {
		return TriggerResult{}, false, nil
	}
	proj, ok, err := e.projects.GetProject(p.ctx, string(p.worker.ProjectID))
	if err != nil {
		slog.Warn("review carry: project unavailable; reviewing normally", "session", p.workerID, "err", err)
		return TriggerResult{}, false, nil
	}
	if !ok || !proj.Config.ApproveIdenticalPatch {
		return TriggerResult{}, false, nil
	}
	runs := reviewRunsForHarness(p.runs, p.harness)
	for _, pr := range p.prs {
		if pr.Merged || pr.Closed || pr.Draft || pr.HeadSHA == "" || prHasRunAt(runs, pr) {
			continue
		}
		prior, found := latestVerdictRun(p.runs, pr.URL)
		// Any reviewer's approval carries: the risk pick may name a stronger
		// reviewer than the one that approved, but an identical patch needs no
		// second review (B546).
		if !found ||
			domain.TallpineReviewGateForHead(p.runs, pr.URL, prior.TargetSHA) != domain.TallpineReviewGateApproved {
			continue
		}
		// An approval on this very head by another reviewer is a finished review,
		// not a rebase: a requested second opinion must still run.
		if prior.TargetSHA == pr.HeadSHA {
			continue
		}
		same, err := e.samePatch(p.ctx, p.worker.Metadata.WorkspacePath, pr, prior.TargetSHA)
		if err != nil {
			slog.Warn("review carry: patch comparison unavailable", "session", p.workerID, "pr", pr.URL, "err", err)
			continue
		}
		if !same {
			continue
		}
		now := e.clock()
		carried := domain.ReviewRun{
			ID: e.newID(), ReviewID: prior.ReviewID, SessionID: p.workerID, Harness: p.harness,
			Model: carriedModel(prior, p.harness), TriggerSource: p.source, PRURL: pr.URL, TargetSHA: pr.HeadSHA,
			Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved,
			// The approval stands on the prior pass's posted review, so this run links
			// to it and does not read as a review that failed to post.
			GithubReviewID: prior.GithubReviewID,
			Body:           fmt.Sprintf("Approval carried over from %s: this head has an identical patch (git patch-id), so it was not reviewed again.", shortSHA(prior.TargetSHA)),
			CreatedAt:      now, DeliveredAt: &now,
		}
		if err := e.store.InsertReviewRun(p.ctx, carried); err != nil {
			return TriggerResult{}, false, err
		}
		p.runs = append(p.runs, carried)
	}
	return TriggerResult{}, false, nil
}

func prHasRunAt(runs []domain.ReviewRun, pr domain.PullRequest) bool {
	for _, r := range runs {
		if r.PRURL == pr.URL && r.TargetSHA == pr.HeadSHA {
			return true
		}
	}
	return false
}

// latestVerdictRun returns the newest run on the PR that reached a verdict, from
// any reviewer. A later changes-requested therefore blocks carrying an older
// approval.
func latestVerdictRun(runs []domain.ReviewRun, prURL string) (domain.ReviewRun, bool) {
	var best domain.ReviewRun
	found := false
	for _, r := range runs {
		if r.PRURL != prURL || r.Verdict == "" || r.Verdict == domain.VerdictNone {
			continue
		}
		if !found || r.CreatedAt.After(best.CreatedAt) || (r.CreatedAt.Equal(best.CreatedAt) && r.ID > best.ID) {
			best, found = r, true
		}
	}
	return best, found
}

func (e *Engine) samePatch(ctx stdctx.Context, workspace string, pr domain.PullRequest, priorSHA string) (bool, error) {
	base := prDiffBase(pr)
	if base == "" || priorSHA == "" {
		return false, fmt.Errorf("pull request has no base or earlier reviewed commit")
	}
	cur, err := e.patchID(ctx, workspace, base, pr.HeadSHA)
	if err != nil || cur == "" {
		return false, err
	}
	old, err := e.patchID(ctx, workspace, base, priorSHA)
	if err != nil {
		return false, err
	}
	return cur == old, nil
}

// gitPatchID returns the stable patch-id of the change base...sha makes, empty
// for an empty diff.
func gitPatchID(ctx stdctx.Context, workspace, base, sha string) (string, error) {
	diff, err := process.CommandContext(ctx, "git", "-C", workspace, "diff", base+"..."+sha).Output()
	if err != nil {
		return "", err
	}
	cmd := process.CommandContext(ctx, "git", "-C", workspace, "patch-id", "--stable")
	cmd.Stdin = strings.NewReader(string(diff))
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
}

// carriedModel keeps the prior run's model only when the same reviewer carries
// it; another harness does not run that model.
func carriedModel(prior domain.ReviewRun, harness domain.ReviewerHarness) string {
	if prior.Harness == harness {
		return prior.Model
	}
	return ""
}
