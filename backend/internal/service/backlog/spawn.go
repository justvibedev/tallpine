package backlog

import (
	"context"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// SpawnItem is a backlog item prepared for `tallpine spawn --backlog`.
type SpawnItem struct {
	ID  string
	Ref string
	// ParentID is the id of the item's parent, "" for a top-level item. A
	// worker started on the item joins the parent's orchestrator lane.
	ParentID string
	// Context is the task context appended to the worker's prompt.
	Context string
	// PlanID is the plan reserved for an in-place implementer (B335), "" when
	// the item is not planned in place. The caller attaches the spawned session
	// with AttachInPlaceAuthor, or releases the plan if the spawn fails.
	PlanID string
	// Warning is non-empty when the item has open blockers and force started it anyway.
	Warning string
}

// PrepareSpawn validates that work may start on the item and builds the
// worker's task context. The item must be open and not a parent; a held item
// needs force. Open v.OpenBlockers refuse the spawn unless force is set, so
// stacked work cannot run in parallel and conflict; a forced spawn warns.
func (s *Service) PrepareSpawn(ctx context.Context, projectID domain.ProjectID, ref string, force bool) (SpawnItem, error) {
	return s.PrepareSpawnAllowDuplicate(ctx, projectID, ref, force, false)
}

// PrepareSpawnAllowDuplicate is PrepareSpawn with an explicit override of the
// live-holder guard; force (held items) deliberately does not imply it.
func (s *Service) PrepareSpawnAllowDuplicate(ctx context.Context, projectID domain.ProjectID, ref string, force, allowDuplicate bool) (SpawnItem, error) {
	return s.PrepareSpawnPlan(ctx, projectID, ref, force, allowDuplicate, false, PlanArmSeparate)
}

// AttachInPlaceAuthor records the spawned implementer as the author of the
// plan PrepareSpawnPlan reserved.
func (s *Service) AttachInPlaceAuthor(ctx context.Context, planID string, sid domain.SessionID) error {
	return s.plans.AttachAuthor(ctx, planID, sid)
}

// ReleaseInPlace fails a reserved plan whose implementer did not start.
func (s *Service) ReleaseInPlace(ctx context.Context, planID, reason string) {
	s.plans.ReleaseInPlace(ctx, planID, reason)
}

// PrepareSpawnPlan is PrepareSpawnAllowDuplicate with a plan arm (B335):
// "separate" keeps today's behaviour (a plan-first refusal sends the caller to
// `plan start`), "in-place" makes the spawned implementer author the plan, and
// "" picks in-place only when the project's planInPlace switch is on and the
// item needs a plan.
func (s *Service) PrepareSpawnPlan(ctx context.Context, projectID domain.ProjectID, ref string, force, allowDuplicate, allowBlocked bool, arm string) (SpawnItem, error) {
	if !ValidPlanArm(arm) {
		return SpawnItem{}, apierr.Invalid("PLAN_ARM_INVALID", fmt.Sprintf("--plan must be %s or %s", PlanArmInPlace, PlanArmSeparate), nil)
	}
	if arm == PlanArmInPlace && !s.plans.Enabled() {
		return SpawnItem{}, errPlannerDisabled()
	}
	item, err := s.resolve(ctx, projectID, ref)
	if err != nil {
		return SpawnItem{}, err
	}
	if item.State != domain.BacklogOpen {
		return SpawnItem{}, apierr.Conflict("BACKLOG_ITEM_NOT_OPEN", fmt.Sprintf("%s is %s: only open items can be started", item.Ref(), item.State), nil)
	}
	startable, err := s.IsStartable(ctx, domain.BacklogItem{State: item.State, ID: item.ID})
	if err != nil {
		return SpawnItem{}, err
	}
	if !startable {
		return SpawnItem{}, apierr.Conflict("BACKLOG_ITEM_IS_PARENT", fmt.Sprintf("%s has children: start a child instead", item.Ref()), nil)
	}
	if item.Hold != "" && !force {
		return SpawnItem{}, apierr.Conflict("BACKLOG_ITEM_HELD", fmt.Sprintf("%s is held (%s); pass --force to start it anyway", item.Ref(), item.Hold), nil)
	}
	if !allowDuplicate {
		facts, err := s.store.BacklogFactsFor(ctx, item.ID)
		if err != nil {
			return SpawnItem{}, err
		}
		for _, sess := range facts.Sessions {
			if !sess.Terminated && !s.plans.isPlanner(ctx, domain.SessionID(sess.SessionID)) {
				return SpawnItem{}, apierr.Conflict("BACKLOG_ITEM_HAS_LIVE_SESSION", fmt.Sprintf("%s already has a live session %s; pass --allow-duplicate to start another", item.Ref(), sess.SessionID), nil)
			}
		}
	}
	v, err := s.view(ctx, item)
	if err != nil {
		return SpawnItem{}, err
	}
	if len(v.OpenBlockers) > 0 && !force && !allowBlocked {
		return SpawnItem{}, apierr.Conflict("BACKLOG_ITEM_BLOCKED", fmt.Sprintf("%s is blocked by %s; finish the blocker first or pass --force to start it anyway", item.Ref(), strings.Join(v.OpenBlockers, ", ")), nil)
	}
	var warning string
	if len(v.OpenBlockers) > 0 {
		warning = fmt.Sprintf("%s is blocked by %s", item.Ref(), strings.Join(v.OpenBlockers, ", "))
	}
	scope := s.effectiveScope(ctx, item)
	out := SpawnItem{ID: item.ID, Ref: item.Ref(), ParentID: item.ParentID, Context: spawnContext(item) + fmt.Sprintf("\n\nPublishing: %s", publishingBrief(scope)), Warning: warning}
	if s.plans.Enabled() {
		extra, planID, err := s.planForSpawn(ctx, item, force, arm)
		if err != nil {
			return SpawnItem{}, err
		}
		out.Context += extra
		out.PlanID = planID
	}
	if scope == "" {
		note := fmt.Sprintf("%s has no scope and no open run sets one: the worker gets no publishing permission (set one with backlog edit %s --scope pr)", item.Ref(), item.Ref())
		if out.Warning != "" {
			out.Warning += "; " + note
		} else {
			out.Warning = note
		}
	}
	return out, nil
}

// LinkSession records that a session works on the item. createdBy is the
// spawning session, or "human".
func (s *Service) LinkSession(ctx context.Context, itemID string, sessionID domain.SessionID, createdBy string) error {
	if createdBy == "" {
		createdBy = "human"
	}
	_, err := s.store.AddBacklogLink(ctx, itemID, domain.BacklogLink{Kind: domain.BacklogLinkSession, Ref: string(sessionID), CreatedBy: createdBy}, s.now().UTC())
	return err
}

// spawnContext is the backlog section of a worker's task prompt: the item's
// title, note, done-when and the orchestrator's notes. Agent notes reach the
// item's workers only; they are never exported.
func spawnContext(i domain.BacklogItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Backlog item %s: %s\n", i.Ref(), i.Title)
	if n := strings.TrimSpace(i.Note); n != "" {
		fmt.Fprintf(&b, "\nNote:\n%s\n", n)
	}
	if d := strings.TrimSpace(i.DoneWhen); d != "" {
		fmt.Fprintf(&b, "\nDone when:\n%s\n", d)
	}
	if a := strings.TrimSpace(i.AgentNotes); a != "" {
		fmt.Fprintf(&b, "\nOrchestrator notes:\n%s\n", a)
	}
	return strings.TrimRight(b.String(), "\n")
}

// effectiveScope is the item's scope, else the scope of the project's open
// run (an item's own scope only overrides the run's), else "".
func (s *Service) effectiveScope(ctx context.Context, item domain.BacklogItem) domain.BacklogScope {
	if item.Scope != "" {
		return item.Scope
	}
	rs, ok := s.store.(RunStore)
	if !ok {
		return ""
	}
	runs, err := rs.ListBacklogRuns(ctx, item.ProjectID, true, 1)
	if err != nil || len(runs) == 0 {
		return ""
	}
	run := runs[0]
	if run.SelectionKind == domain.BacklogRunSelectAll {
		return run.Scope
	}
	// Once a plan is saved its items are the run's source of truth (they
	// include children that replaced a planned item).
	if items, err := rs.ListBacklogRunItems(ctx, run.ID); err == nil {
		for _, ri := range items {
			if ri.ItemID == item.ID {
				return run.Scope
			}
		}
	}
	// Otherwise the selection covers the item or one of its ancestors.
	selected := map[string]bool{}
	for _, r := range run.Selection {
		selected[strings.ToUpper(strings.TrimSpace(r))] = true
	}
	for cur, depth := item, 0; depth < 4; depth++ {
		if run.SelectionKind == domain.BacklogRunSelectRefs && selected[strings.ToUpper(cur.Ref())] {
			return run.Scope
		}
		if cur.ParentID == "" {
			break
		}
		parent, ok, err := s.store.GetBacklogItem(ctx, cur.ParentID)
		if err != nil || !ok {
			break
		}
		if selected[strings.ToUpper(parent.Ref())] {
			return run.Scope
		}
		cur = parent
	}
	return ""
}

// publishingBrief is the worker's publishing permission for an item scope.
func publishingBrief(scope domain.BacklogScope) string {
	switch scope {
	case domain.BacklogScopePR:
		return "authorized. Push your branch and open a PR when the work is verified; do not merge it."
	case domain.BacklogScopePRMerge:
		return "authorized. Push your branch and open a PR when the work is verified; merge it once it is approved and the project allows."
	case domain.BacklogScopeLocal:
		return "local, no PR. Commit your work on your branch, do not push and do not open a PR, then report ready (tallpine report --needs-input) with the changed files and tests. Your branch gets a Tallpine native review and is landed locally."
	default:
		return "not set on the item and no run scope applies. Do not push or open a PR unless your brief or the project rules grant it; ask before publishing."
	}
}

// ResolveLane returns the id of the open parent item an orchestrator lane is
// bound to. A lane must be a parent (or top-level) item, not a leaf task.
func (s *Service) ResolveLane(ctx context.Context, projectID domain.ProjectID, ref string) (string, error) {
	id, closed, err := s.ResolveLaneItem(ctx, projectID, ref)
	if err != nil {
		return "", err
	}
	if closed != "" {
		return "", apierr.Conflict("BACKLOG_LANE_CLOSED", fmt.Sprintf("%s is %s: pick an open item as the lane", ref, closed), nil)
	}
	return id, nil
}

// ResolveLaneItem is ResolveLane without the closed-item refusal: it returns
// the lane item's id and, when the item is closed or dropped, its state. It
// lets callers still reach a lane orchestrator that outlived its item.
func (s *Service) ResolveLaneItem(ctx context.Context, projectID domain.ProjectID, ref string) (string, domain.BacklogState, error) {
	item, err := s.resolve(ctx, projectID, ref)
	if err != nil {
		return "", "", err
	}
	if item.ParentID != "" {
		return "", "", apierr.Conflict("BACKLOG_LANE_NOT_TOP_LEVEL", fmt.Sprintf("%s is a child item: a lane is a top-level topic item", item.Ref()), nil)
	}
	if item.State.Closed() {
		return item.ID, item.State, nil
	}
	return item.ID, "", nil
}

// planForSpawn applies the plan rules of starting work on an item and returns
// the plan section of the task prompt. An approved plan joins the task, from
// the item or, for a child made from a PR split, its parent. With planning mode
// always, no approved plan is a refusal ("plan first"); an approved plan whose
// planned files changed on the default branch is a refusal too, until the owner
// re-plans or builds anyway. force skips both refusals.
func (s *Service) planForSpawn(ctx context.Context, item domain.BacklogItem, force bool, arm string) (string, string, error) {
	owner, plan, ok, err := s.planOwner(ctx, item)
	if err != nil {
		return "", "", err
	}
	if !ok || plan.Status != domain.PlanApproved {
		refusal := s.planRequiredRefusal(ctx, item, plan, ok, force)
		return s.inPlaceOrRefuse(ctx, item, plan, ok, arm, refusal)
	}
	extra, err := s.approvedPlanContext(ctx, item, owner, plan, force)
	return extra, "", err
}

// inPlaceOrRefuse decides what an item without an approved plan gets: today's
// outcome (refusal, or none), or a reserved in-place plan (B335).
func (s *Service) inPlaceOrRefuse(ctx context.Context, item domain.BacklogItem, plan domain.BacklogPlan, hasPlan bool, arm string, refusal error) (string, string, error) {
	on := s.plans.InPlaceEnabled(ctx, item.ProjectID)
	switch {
	case arm == PlanArmInPlace && !on:
		return "", "", apierr.Conflict("PLAN_IN_PLACE_OFF", "plan-in-place is off for this project (set planInPlace, unless a managed policy locks it off)", nil)
	case arm == PlanArmSeparate, arm == "" && (!on || refusal == nil):
		return "", "", refusal
	}
	if hasPlan && plan.Status.Open() {
		return "", "", planOpenErr(item.Ref())
	}
	reserved, err := s.plans.ReserveInPlace(ctx, item, spawnerFrom(ctx))
	if err != nil {
		return "", "", err
	}
	return "\n\n" + inPlaceBrief(item.Ref()), reserved.ID, nil
}

// approvedPlanContext is the plan section of the task prompt for an item whose
// governing plan is approved.
func (s *Service) approvedPlanContext(ctx context.Context, item, owner domain.BacklogItem, plan domain.BacklogPlan, force bool) (string, error) {
	// The child's entry in its parent's PR split, computed once for the
	// staleness check and the prompt.
	entry, hasEntry := 0, false
	if owner.ID != item.ID {
		entry, hasEntry = planEntry(item, len(plan.PRSplit))
	}
	if !force && s.plans.staleSplit(ctx, narrowedForChild(plan, owner.ID != item.ID, entry, hasEntry)) {
		return "", apierr.Conflict("PLAN_STALE", fmt.Sprintf("the plan for %s is stale: files it names changed on the default branch since it was made. Re-plan (`tallpine plan replan %s`) or build anyway (`tallpine plan build-anyway %s`), or pass --force", owner.Ref(), owner.Ref(), owner.Ref()), nil)
	}
	out := "\n\n" + planSpawnContext(plan, owner.Ref())
	if hasEntry {
		out += fmt.Sprintf("\n\nYou build PR %d of %d of this plan: %s", entry+1, len(plan.PRSplit), plan.PRSplit[entry].Title)
	}
	return out, nil
}

// planOwner finds the plan that governs item: its own current plan or, when
// that is missing or not approved, the approved plan of its parent (a child
// made from a PR split). ok reports whether plan is a real plan.
func (s *Service) planOwner(ctx context.Context, item domain.BacklogItem) (owner domain.BacklogItem, plan domain.BacklogPlan, ok bool, err error) {
	owner = item
	plan, ok, err = s.plans.store.CurrentBacklogPlanForItem(ctx, item.ID)
	if err != nil {
		return owner, plan, false, err
	}
	if (ok && plan.Status == domain.PlanApproved) || item.ParentID == "" {
		return owner, plan, ok, nil
	}
	parent, pok, err := s.store.GetBacklogItem(ctx, item.ParentID)
	if err != nil || !pok {
		return owner, plan, ok, err
	}
	pp, found, err := s.plans.store.CurrentBacklogPlanForItem(ctx, parent.ID)
	if err != nil {
		return owner, plan, ok, err
	}
	if found && pp.Status == domain.PlanApproved {
		return parent, pp, true, nil
	}
	return owner, plan, ok, nil
}

// planRequiredRefusal is the "plan first" refusal when planning mode is
// always and item has no approved plan; nil otherwise (or with force).
func (s *Service) planRequiredRefusal(ctx context.Context, item domain.BacklogItem, plan domain.BacklogPlan, hasPlan, force bool) error {
	if force || s.plans.modeFor(ctx, item.ProjectID) != domain.PlanningAlways {
		return nil
	}
	why := "has no plan"
	if hasPlan && plan.Status.Open() {
		why = "has a plan that is not approved yet"
	}
	return apierr.Conflict("PLAN_REQUIRED", fmt.Sprintf("plan first: %s %s and this project plans before it builds. Run `tallpine plan start %s`, or pass --force to build without a plan", item.Ref(), why, item.Ref()), nil)
}

// narrowedForChild is the plan to check for staleness. A child is stale only
// by its own PR's files: the earlier PRs of the plan merging is the plan
// working, not the plan going out of date. A child with no known entry has
// nothing to compare, so no stale refusal.
func narrowedForChild(plan domain.BacklogPlan, isChild bool, entry int, hasEntry bool) domain.BacklogPlan {
	if !isChild {
		return plan
	}
	if hasEntry {
		plan.PRSplit = []domain.PlanPR{plan.PRSplit[entry]}
	} else {
		plan.BuildAnyway = true
	}
	return plan
}
