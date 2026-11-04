package review

import (
	stdctx "context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/process"
)

// reviewerSelection resolves which reviewer reviews the worker's PR: a
// persisted session preference wins, then the project's reviewer config, then
// the project's default worker config, then the worker's own harness when
// supported, otherwise claude-code.
func (e *Engine) reviewerSelection(
	ctx stdctx.Context,
	worker domain.SessionRecord,
) (domain.ReviewerHarness, domain.AgentConfig, error) {
	harness, config, err := e.selectReviewer(ctx, worker)
	if err != nil {
		return "", domain.AgentConfig{}, err
	}
	// Saved session or project reviewer config may still name a removed
	// reviewer. Its model and effort were tuned for that harness, so drop them.
	if resolved, replaced := harness.OrFallback(); replaced {
		slog.Warn("reviewer config names an unsupported harness; falling back",
			"session", worker.ID, "project", worker.ProjectID, "configured", harness, "fallback", resolved)
		return resolved, domain.AgentConfig{}, nil
	}
	return harness, config, nil
}

func (e *Engine) selectReviewer(
	ctx stdctx.Context,
	worker domain.SessionRecord,
) (domain.ReviewerHarness, domain.AgentConfig, error) {
	if worker.ReviewerHarness != "" || !worker.ReviewerConfig.IsZero() {
		harness := worker.ReviewerHarness
		projectHarness, projectConfig, err := e.projectReviewerSelection(ctx, worker)
		if err != nil {
			return "", domain.AgentConfig{}, err
		}
		if harness == "" {
			harness = projectHarness
		}
		baseConfig := domain.AgentConfig{}
		if harness == projectHarness {
			baseConfig = projectConfig
		}
		return harness, mergeReviewerAgentConfig(baseConfig, worker.ReviewerConfig), nil
	}
	return e.projectReviewerSelection(ctx, worker)
}

func mergeReviewerAgentConfig(base, override domain.AgentConfig) domain.AgentConfig {
	if override.Model != "" {
		base.Model = override.Model
	}
	if override.Effort != "" {
		base.Effort = override.Effort
	}
	if override.Mode != "" {
		base.Mode = override.Mode
	}
	if override.Permissions != "" {
		base.Permissions = override.Permissions
	}
	return base
}

func (e *Engine) projectReviewerSelection(
	ctx stdctx.Context,
	worker domain.SessionRecord,
) (domain.ReviewerHarness, domain.AgentConfig, error) {
	var cfg domain.ProjectConfig
	if e.projects != nil {
		if proj, ok, err := e.projects.GetProject(ctx, string(worker.ProjectID)); err != nil {
			return "", domain.AgentConfig{}, err
		} else if ok {
			cfg = proj.Config
		}
	}
	if len(cfg.Reviewers) > 0 {
		return cfg.Reviewers[0].Harness, cfg.Reviewers[0].AgentConfig, nil
	}
	harness, config := cfg.DefaultWorkerReviewer()
	if harness != "" {
		return harness, config, nil
	}
	return cfg.ResolveReviewerHarness(worker.Harness), domain.AgentConfig{}, nil
}

// checkModelPolicy refuses a reviewer model the project's model policy denies.
func (e *Engine) checkModelPolicy(ctx stdctx.Context, id domain.ProjectID, model string) error {
	if e.projects == nil || strings.TrimSpace(model) == "" {
		return nil
	}
	project, ok, err := e.projects.GetProject(ctx, string(id))
	if err != nil || !ok {
		return err
	}
	_, err = project.Config.ResolveModel("", model)
	return err
}

func (e *Engine) projectEnv(ctx stdctx.Context, id domain.ProjectID, harness domain.ReviewerHarness) (map[string]string, error) {
	if e.projects == nil {
		return nil, nil
	}
	project, ok, err := e.projects.GetProject(ctx, string(id))
	if err != nil || !ok {
		return nil, err
	}
	env := project.Config.Env
	if harness != domain.ReviewerClaudeCode {
		return env, nil
	}
	if _, set := env[domain.EnvPromptCacheTTL]; set || os.Getenv(domain.EnvPromptCacheTTL) != "" {
		return env, nil
	}
	ttl := project.Config.PromptCacheTTLFor(domain.RoleReviewer)
	if ttl == "" {
		return env, nil
	}
	out := make(map[string]string, len(env)+1)
	for k, v := range env {
		out[k] = v
	}
	out[domain.EnvPromptCacheTTL] = ttl
	return out, nil
}

func (e *Engine) upsertReview(ctx stdctx.Context, worker domain.SessionRecord, harness domain.ReviewerHarness, handleID, agentSessionID, reviewerLaunchID string, activityState domain.ActivityState, now time.Time) (domain.Review, error) {
	existing, ok, err := e.store.GetReviewBySessionAndHarness(ctx, worker.ID, harness)
	if err != nil {
		return domain.Review{}, err
	}
	agentSessionID = strings.TrimSpace(agentSessionID)
	review := domain.Review{
		ID:                    e.newID(),
		SessionID:             worker.ID,
		ProjectID:             worker.ProjectID,
		Harness:               harness,
		PRURL:                 "",
		ReviewerHandleID:      handleID,
		AgentSessionID:        agentSessionID,
		ReviewerLaunchID:      strings.TrimSpace(reviewerLaunchID),
		ReviewerActivityState: activityState,
		InterfaceMode:         domain.ReviewerInterfaceTUI,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if ok {
		// Reuse the existing row's identity and creation time; UpsertReview
		// refreshes harness/pr_url/reviewer_handle_id/updated_at.
		review.ID = existing.ID
		review.IsArchived = existing.IsArchived
		if existing.IsArchived {
			review.ReviewerActivityState = existing.ReviewerActivityState
		}
		review.CreatedAt = existing.CreatedAt
		if review.ReviewerLaunchID == "" {
			review.ReviewerLaunchID = existing.ReviewerLaunchID
		}
		if review.ReviewerActivityState == "" {
			review.ReviewerActivityState = existing.ReviewerActivityState
		}
		review.InterfaceMode = existing.InterfaceMode
		review.ProviderConversationID = existing.ProviderConversationID
		review.ControllerGeneration = existing.ControllerGeneration
		review.ControllerError = existing.ControllerError
	}
	if err := e.store.UpsertReview(ctx, review); err != nil {
		return domain.Review{}, err
	}
	return review, nil
}

func (e *Engine) setReviewerInterfaceMode(ctx stdctx.Context, reviewID string, mode domain.ReviewerInterfaceMode, now time.Time) error {
	ok, err := e.store.SetReviewInterfaceMode(ctx, reviewID, mode, now)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: reviewer %q", ErrNotFound, reviewID)
	}
	return nil
}

// stopReviewChatWork settles durable work even when no controller survived a
// daemon restart. It runs before replacing the review controller fence.
func (e *Engine) stopReviewChatWork(ctx stdctx.Context, review domain.Review) error {
	if review.InterfaceMode != domain.ReviewerInterfaceChat {
		return nil
	}
	if err := e.launcher.Destroy(ctx, reviewerChatHandlePrefix+review.ID); err != nil {
		return fmt.Errorf("stop previous reviewer Chat: %w", err)
	}
	return e.store.SettleReviewChatWork(ctx, review.ID, e.clock())
}

// beginHarnessUse holds the harness against updates until the caller records
// the reviewer it launches.
func (e *Engine) beginHarnessUse(harness domain.ReviewerHarness) (func(), error) {
	if e.harnessUse == nil {
		return func() {}, nil
	}
	release, ok := e.harnessUse.TryBeginHarnessUse(domain.AgentHarness(harness))
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrHarnessInstallActive, harness)
	}
	return release, nil
}

// riskReviewer is the stronger reviewer picked for a risky change, with the
// due PRs that earned it.
type riskReviewer struct {
	harness domain.ReviewerHarness
	config  domain.AgentConfig
	reason  string
	prURLs  map[string]bool
}

// riskReviewerFor returns the project's stronger reviewer when a PR due for
// review changes a risky path, with the reason to record on the run. A path
// list that cannot be read keeps the configured reviewer rather than guessing.
func (e *Engine) riskReviewerFor(ctx stdctx.Context, worker domain.SessionRecord, reviews []PRReviewState, runs []domain.ReviewRun, due func(PRReviewState) bool) (riskReviewer, bool) {
	var cfg domain.ProjectConfig
	if e.projects != nil {
		if proj, ok, err := e.projects.GetProject(ctx, string(worker.ProjectID)); err == nil && ok {
			cfg = proj.Config
		}
	}
	risk := cfg.ReviewRisk
	globs, harness, model, effort := risk.Resolve()
	prs, err := e.prs.ListPRsBySession(ctx, worker.ID)
	if err != nil {
		return riskReviewer{}, false
	}
	if prs, err = WithLocalTarget(ctx, worker, prs); err != nil {
		return riskReviewer{}, false
	}
	pick := riskReviewer{harness: harness, config: domain.AgentConfig{Model: model, Effort: effort}, prURLs: map[string]bool{}}
	for _, rs := range reviews {
		if rs.Status == ReviewStateIneligible || (due != nil && !due(rs)) {
			continue
		}
		// A PR's first review is one with no finished review on any agent.
		if risk.FirstReviewOnly && hasReviewRun(runs, rs.PRURL) {
			continue
		}
		for _, pr := range prs {
			if pr.URL != rs.PRURL {
				continue
			}
			changed, err := e.changedPaths(ctx, worker.Metadata.WorkspacePath, pr)
			if err != nil {
				slog.Warn("review risk: changed paths unavailable", "session", worker.ID, "pr", pr.URL, "err", err)
				continue
			}
			if file, glob, ok := domain.MatchReviewRisk(globs, changed); ok {
				if pick.reason == "" {
					pick.reason = fmt.Sprintf("risky path %s matches %s", file, glob)
				}
				pick.prURLs[pr.URL] = true
			}
		}
	}
	return pick, len(pick.prURLs) > 0
}

// hasReviewRun reports whether the PR already had a review that finished, so a
// later review is a re-review. A failed or cancelled run produced no review, so
// its retry is still the first one.
func hasReviewRun(runs []domain.ReviewRun, prURL string) bool {
	for _, r := range runs {
		if r.PRURL != prURL {
			continue
		}
		if r.Verdict != "" || r.Status == domain.ReviewRunComplete || r.Status == domain.ReviewRunDelivered {
			return true
		}
	}
	return false
}

// prDiffBase is the ref a PR's own change is measured against: its target
// branch as fetched in the worker's checkout, else the recorded base commit.
func prDiffBase(pr domain.PullRequest) string {
	if pr.TargetBranch != "" {
		return "origin/" + pr.TargetBranch
	}
	return pr.BaseSHA
}

// gitChangedPaths diffs the PR head against its target branch in the worker's
// own checkout, which holds the pushed head.
func gitChangedPaths(ctx stdctx.Context, workspace string, pr domain.PullRequest) ([]string, error) {
	base := prDiffBase(pr)
	if base == "" || pr.HeadSHA == "" {
		return nil, errors.New("pull request has no base or head commit")
	}
	cmd := process.CommandContext(ctx, "git", "-C", workspace, "diff", "--name-only", "-z", base+"..."+pr.HeadSHA)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}
