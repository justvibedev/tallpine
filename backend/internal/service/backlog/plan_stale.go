package backlog

import (
	"context"
	"path"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// PlanDiff names the paths that changed on the project's default branch since
// a commit. The daemon backs it with `git diff --name-only <base>..<default>`.
type PlanDiff interface {
	ChangedSince(ctx context.Context, projectID domain.ProjectID, baseCommit string) ([]string, error)
}

// PlanDiffText is an optional PlanDiff capability: the added and removed lines
// per changed path (`git diff -U0`). With it, a planned file that changed only
// in hunks unrelated to what the plan names does not make the plan stale.
type PlanDiffText interface {
	ChangedLines(ctx context.Context, projectID domain.ProjectID, baseCommit string, paths []string) (map[string][]string, error)
}

// PlanSummary is the plan state an item carries on its views.
type PlanSummary struct {
	ID     string
	Status domain.PlanStatus
	// Stale: the default branch moved past the plan's base commit and touched a
	// path the plan names.
	Stale bool
	// ItemChanged: the item's title, note or done-when was edited after planning began.
	ItemChanged bool
}

// Stale reports whether an approved plan no longer matches the default branch.
// A plan the owner chose to build anyway is never stale. The check is advisory:
// a missing base commit or a failed git call is not stale.
func (p *PlanService) Stale(ctx context.Context, plan domain.BacklogPlan) bool {
	// A plan split into child items is checked per child at spawn: its earlier
	// PRs merging must not make the whole plan look stale.
	if item, ok, err := p.bl.store.GetBacklogItem(ctx, plan.ItemID); err == nil && ok {
		if kids, err := p.bl.planMadeChildren(ctx, item); err == nil && len(kids) > 0 {
			return false
		}
	}
	return p.staleSplit(ctx, plan)
}

// submitHead is the default-branch head at submit, read before the write
// transaction opens (it runs git). The planner read the branch up to now, so
// merges during planning (the plan's own prerequisites) are not drift. A failed
// lookup returns empty and the plan keeps its start commit.
func (p *PlanService) submitHead(ctx context.Context, projectID domain.ProjectID) (commit, branch string) {
	if p.base == nil {
		return "", ""
	}
	if h, b, err := p.base.Base(ctx, projectID); err == nil && h != "" {
		return h, b
	}
	return "", ""
}

// storeSubmission moves the plan to awaiting approval with the submitted
// content and the baseline rebased to the head resolved above.
func (p *PlanService) storeSubmission(ctx context.Context, plan domain.BacklogPlan, in PlanSubmitInput) (domain.BacklogPlan, error) {
	commit, branch := p.submitHead(ctx, plan.ProjectID)
	return p.transition(ctx, plan.ID, domain.PlanAwaitingApproval, func(pl *domain.BacklogPlan) error {
		pl.PlanMarkdown, pl.DecisionsMD, pl.PRSplit, pl.ReviewFocus = in.Plan, in.Decisions, in.PRs, in.Focus
		if commit != "" {
			pl.BaseCommit, pl.BaseBranch = commit, branch
		}
		return nil
	})
}

// staleSplit checks the plan's PR split against the default branch.
func (p *PlanService) staleSplit(ctx context.Context, plan domain.BacklogPlan) bool {
	if !p.Enabled() || p.diff == nil || plan.Status != domain.PlanApproved || plan.BuildAnyway || plan.BaseCommit == "" {
		return false
	}
	changed, err := p.diff.ChangedSince(ctx, plan.ProjectID, plan.BaseCommit)
	if err != nil {
		p.log.Warn("plan stale check", "plan", plan.ID, "error", err)
		return false
	}
	if !plannedPathsChanged(plan.PRSplit, changed) {
		return false
	}
	dt, ok := p.diff.(PlanDiffText)
	if !ok {
		return true
	}
	covered := coveredPaths(plan.PRSplit, changed)
	if len(covered) == 0 {
		return false
	}
	lines, err := dt.ChangedLines(ctx, plan.ProjectID, plan.BaseCommit, covered)
	if err != nil {
		// Advisory like ChangedSince: a transient diff error must not block a spawn.
		p.log.Warn("plan stale hunks", "plan", plan.ID, "error", err)
		return false
	}
	return hunksTouchPlan(plan, covered, lines)
}

// plannedPathsChanged reports whether any changed path falls under a path the
// plan's PR split names. A plan that names no files has nothing to compare and
// is never stale.
func plannedPathsChanged(split []domain.PlanPR, changed []string) bool {
	if len(changed) == 0 {
		return false
	}
	var named []string
	for _, pr := range split {
		for _, f := range pr.Files {
			if f = strings.TrimSpace(f); f != "" {
				named = append(named, f)
			}
		}
	}
	if len(named) == 0 {
		return false
	}
	for _, c := range changed {
		for _, n := range named {
			if pathCovers(n, c) {
				return true
			}
		}
	}
	return false
}

// pathCovers: the planned path is the changed file, a directory holding it, or
// a glob matching it.
func pathCovers(planned, changed string) bool {
	planned = strings.TrimPrefix(planned, "./")
	if planned == changed {
		return true
	}
	if dir := strings.TrimSuffix(planned, "/"); strings.HasPrefix(changed, dir+"/") {
		return true
	}
	if strings.ContainsAny(planned, "*?[") {
		ok, err := path.Match(planned, changed)
		return err == nil && ok
	}
	return false
}

// Summary returns the item's plan summary, or nil while the switch is off or
// the item has no plan. Stale needs git, so only single-item reads ask for it.
func (p *PlanService) Summary(ctx context.Context, itemID string, withStale bool) *PlanSummary {
	if !p.Enabled() {
		return nil
	}
	plan, ok, err := p.store.CurrentBacklogPlanForItem(ctx, itemID)
	if err != nil || !ok {
		return nil
	}
	s := &PlanSummary{ID: plan.ID, Status: plan.Status, ItemChanged: !plan.ItemChangedAt.IsZero()}
	if withStale {
		s.Stale = p.Stale(ctx, plan)
	}
	return s
}

// ItemEdited flags the item's open or approved plan after a title, note or
// done-when edit. It does not restart the plan.
func (p *PlanService) ItemEdited(ctx context.Context, itemID string) {
	if !p.Enabled() {
		return
	}
	plan, ok, err := p.store.CurrentBacklogPlanForItem(ctx, itemID)
	if err != nil || !ok || plan.Status == domain.PlanFailed || plan.Status == domain.PlanSuperseded {
		return
	}
	_, err = p.store.UpdateBacklogPlan(ctx, plan.ID, func(pl *domain.BacklogPlan) error {
		pl.ItemChangedAt, pl.UpdatedAt = p.now().UTC(), p.now().UTC()
		return nil
	})
	if err != nil {
		p.log.Warn("flag plan item changed", "plan", plan.ID, "error", err)
	}
}

// ItemClosed supersedes the item's open plan when the item is done or dropped,
// and stops its planner. An approved plan stays as the record.
func (p *PlanService) ItemClosed(ctx context.Context, itemID string) {
	if !p.Enabled() {
		return
	}
	open, ok, err := p.store.OpenBacklogPlanForItem(ctx, itemID)
	if err != nil || !ok {
		return
	}
	if _, err := p.transition(ctx, open.ID, domain.PlanSuperseded, nil); err != nil {
		p.log.Warn("supersede plan of closed item", "plan", open.ID, "error", err)
		return
	}
	p.stopPlanner(ctx, open)
}

// modeFor is the project's planning mode; auto when no source is wired.
func (p *PlanService) modeFor(ctx context.Context, projectID domain.ProjectID) domain.PlanningMode {
	if p.mode == nil {
		return domain.PlanningAuto
	}
	return p.mode(ctx, projectID).WithDefault()
}
