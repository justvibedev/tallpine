package backlog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// PlanStore is the plan service's persistence surface; the SQLite store
// satisfies it.
type PlanStore interface {
	CreateBacklogPlan(ctx context.Context, p domain.BacklogPlan) (domain.BacklogPlan, bool, error)
	GetBacklogPlan(ctx context.Context, id string) (domain.BacklogPlan, bool, error)
	OpenBacklogPlanForItem(ctx context.Context, itemID string) (domain.BacklogPlan, bool, error)
	CurrentBacklogPlanForItem(ctx context.Context, itemID string) (domain.BacklogPlan, bool, error)
	SupersededBacklogPlansForItem(ctx context.Context, itemID string) ([]domain.BacklogPlan, error)
	BacklogPlanByPlanner(ctx context.Context, sessionID domain.SessionID) (domain.BacklogPlan, bool, error)
	OpenBacklogPlansForProject(ctx context.Context, projectID domain.ProjectID) ([]domain.BacklogPlan, error)
	UpdateBacklogPlan(ctx context.Context, id string, mutate func(*domain.BacklogPlan) error) (domain.BacklogPlan, error)
	BacklogPlanMessageSource(ctx context.Context, messageID string) (store.PlanMessageSource, bool, error)
	LatestHumanProjectMessage(ctx context.Context, projectID domain.ProjectID) (string, time.Time, bool, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// PlannerSpawn describes the planner session to start for a plan.
type PlannerSpawn struct {
	ProjectID   domain.ProjectID
	ItemID      string
	ItemRef     string
	PlanID      string
	DisplayName string
	// LaneItemID is the item's parent: an in-place implementer joins its lane,
	// as a worker started with `spawn --backlog` does.
	LaneItemID string
	// Prompt is the planner's task: the item and, on a respawn, the record so far.
	Prompt string
}

// PlannerSessions starts and messages planner sessions; the daemon wires the
// session manager behind it.
type PlannerSessions interface {
	// SpawnPlanner starts a read-only planner worker (role planner).
	SpawnPlanner(ctx context.Context, in PlannerSpawn) (domain.SessionID, error)
	// PlannerLive reports whether the planner session can still take a message.
	PlannerLive(ctx context.Context, id domain.SessionID) bool
	// TerminatePlanner stops a planner session. Best effort: an ended session is fine.
	TerminatePlanner(ctx context.Context, id domain.SessionID) error
	// SpawnInPlace starts an implementer (role worker) that authors the plan
	// itself, with the record in its brief (B335).
	SpawnInPlace(ctx context.Context, in PlannerSpawn) (domain.SessionID, error)
	// SendToPlanner delivers a message to a live planner. It reports false when
	// the session has ended, so the caller can start a new one.
	SendToPlanner(ctx context.Context, id domain.SessionID, message string) (bool, error)
}

// PlanBase names the commit a plan is based on: the project's default branch.
type PlanBase interface {
	Base(ctx context.Context, projectID domain.ProjectID) (commit, branch string, err error)
}

// PlanDeps configures a PlanService.
type PlanDeps struct {
	Backlog  *Service
	Store    PlanStore
	Sessions PlannerSessions
	Notify   PlanNotifier
	// NotifyOwner tells the human a plan waits on them (a stored notification,
	// and with it the push). nil skips it. itemRef and itemTitle name the item.
	NotifyOwner func(ctx context.Context, plan domain.BacklogPlan, itemRef, itemTitle string)
	// ClearOwner closes that notification once the plan no longer waits on
	// the owner (answered, approved, superseded, failed). nil skips it.
	ClearOwner func(ctx context.Context, plan domain.BacklogPlan)
	Base       PlanBase
	// Diff powers the stale check; nil turns it off.
	Diff PlanDiff
	// Mode reads the project's planning mode; nil means auto. Always makes a
	// plan a precondition of starting work on an item.
	Mode func(ctx context.Context, projectID domain.ProjectID) domain.PlanningMode
	// AutoApprove reports whether the project approves submitted plans itself
	// (ProjectConfig.PlanAutoApprove, unless a managed policy locks it off);
	// nil means never.
	AutoApprove func(ctx context.Context, projectID domain.ProjectID) bool
	// InPlace reports whether the project lets an implementer plan its own item
	// (ProjectConfig.PlanInPlace, unless a managed policy locks it off); nil
	// means never.
	InPlace func(ctx context.Context, projectID domain.ProjectID) bool
	// Eval reports whether the project is marked eval (ProjectConfig.Eval): its
	// orchestrator is the owner's proxy there, so an orchestrator approves
	// without the owner's chat message. Nil means no project is.
	Eval func(ctx context.Context, projectID domain.ProjectID) bool
	// Enabled is the TALLPINE_PLANNER switch. Off, every call answers disabled.
	Enabled bool
	NewID   func() string
	Now     func() time.Time
	Log     *slog.Logger
}

// PlanService owns plan records: the planner's questions and plan, the owner's
// answers, and approval (B205).
type PlanService struct {
	bl       *Service
	store    PlanStore
	sessions PlannerSessions
	notify   PlanNotifier
	// notifyOwner: see PlanDeps.NotifyOwner.
	notifyOwner func(ctx context.Context, plan domain.BacklogPlan, itemRef, itemTitle string)
	clearOwner  func(ctx context.Context, plan domain.BacklogPlan)
	base        PlanBase
	diff        PlanDiff
	mode        func(ctx context.Context, projectID domain.ProjectID) domain.PlanningMode
	enabled     bool
	// autoApprove: see PlanDeps.AutoApprove.
	autoApprove func(ctx context.Context, projectID domain.ProjectID) bool
	// inPlace: see PlanDeps.InPlace.
	inPlace func(ctx context.Context, projectID domain.ProjectID) bool
	// eval: see PlanDeps.Eval.
	eval  func(ctx context.Context, projectID domain.ProjectID) bool
	newID func() string
	now   func() time.Time
	log   *slog.Logger
	// observer hears every plan status change (backlog runs, B205 PR 6).
	observer func(ctx context.Context, plan domain.BacklogPlan)
	// cancelObserver hears a plan the owner cancelled (no successor follows).
	cancelObserver func(ctx context.Context, plan domain.BacklogPlan)
}

// NewPlanService builds the plan service and registers it with the backlog
// service, so a planner's session end and done report are handled as a planner's.
func NewPlanService(d PlanDeps) *PlanService {
	if d.NewID == nil {
		d.NewID = func() string { return "plan_" + uuid.NewString() }
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	p := &PlanService{bl: d.Backlog, store: d.Store, sessions: d.Sessions, notify: d.Notify, notifyOwner: d.NotifyOwner, clearOwner: d.ClearOwner, base: d.Base, diff: d.Diff, mode: d.Mode, autoApprove: d.AutoApprove, inPlace: d.InPlace, eval: d.Eval, enabled: d.Enabled, newID: d.NewID, now: d.Now, log: d.Log}
	if d.Backlog != nil {
		d.Backlog.plans = p
	}
	return p
}

// SetObserver registers the one listener told after each plan status change
// (created, questions, answers, plan, approval, failure). It runs on the
// caller's goroutine, after the change is stored.
func (p *PlanService) SetObserver(f func(ctx context.Context, plan domain.BacklogPlan)) {
	p.observer = f
}

// SetCancelObserver registers the hook for a plan the owner cancelled. Cancel
// has no successor plan, unlike a replan, so it needs its own signal.
func (p *PlanService) SetCancelObserver(f func(ctx context.Context, plan domain.BacklogPlan)) {
	p.cancelObserver = f
}

func (p *PlanService) changed(ctx context.Context, plan domain.BacklogPlan) {
	if p.clearOwner != nil && plan.PlannerSessionID != "" && !plan.Status.WaitsOnOwner() {
		p.clearOwner(ctx, plan)
	}
	if p.observer != nil {
		p.observer(ctx, plan)
	}
}

// Mode is the project's planning mode (auto when none is configured).
func (p *PlanService) Mode(ctx context.Context, projectID domain.ProjectID) domain.PlanningMode {
	return p.modeFor(ctx, projectID)
}

// Current returns the item's current plan, if any.
func (p *PlanService) Current(ctx context.Context, itemID string) (domain.BacklogPlan, bool, error) {
	return p.store.CurrentBacklogPlanForItem(ctx, itemID)
}

// Enabled reports whether the planner switch is on.
func (p *PlanService) Enabled() bool { return p != nil && p.enabled }

// ErrPlannerDisabled is returned while TALLPINE_PLANNER is off.
func errPlannerDisabled() error {
	return apierr.NotFound("PLANNER_DISABLED", "The planner is off (TALLPINE_PLANNER on this daemon)")
}

func (p *PlanService) guard() error {
	if !p.Enabled() {
		return errPlannerDisabled()
	}
	return nil
}

// decider reports whether the actor may start, answer, approve or replan: the
// owner or the project's orchestrator.
func decider(a Actor) bool {
	return a.Kind == domain.BacklogOriginHuman || a.Kind == domain.BacklogOriginOrchestrator
}

// notifyTarget is the session a plan's notices go to: the orchestrator that
// started it. A plan the owner started has none and falls back to the
// project's main orchestrator.
func notifyTarget(a Actor) domain.SessionID {
	if a.Kind == domain.BacklogOriginOrchestrator {
		return domain.SessionID(a.SessionID)
	}
	return ""
}

func notDecider() error {
	return apierr.Forbidden("PLAN_ACTOR_FORBIDDEN", "Only the owner or the project orchestrator can do this")
}

// Get returns the item's current plan, if any.
func (p *PlanService) Get(ctx context.Context, projectID domain.ProjectID, ref string) (domain.BacklogPlan, error) {
	if err := p.guard(); err != nil {
		return domain.BacklogPlan{}, err
	}
	item, err := p.bl.resolve(ctx, projectID, ref)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	plan, ok, err := p.store.CurrentBacklogPlanForItem(ctx, item.ID)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if !ok {
		return domain.BacklogPlan{}, apierr.NotFound("PLAN_NOT_FOUND", fmt.Sprintf("%s has no plan", item.Ref()))
	}
	return plan, nil
}

// GetByID returns one plan of the item by plan id, superseded ones included.
func (p *PlanService) GetByID(ctx context.Context, projectID domain.ProjectID, ref, planID string) (domain.BacklogPlan, error) {
	if err := p.guard(); err != nil {
		return domain.BacklogPlan{}, err
	}
	item, err := p.bl.resolve(ctx, projectID, ref)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	plan, ok, err := p.store.GetBacklogPlan(ctx, planID)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if !ok || plan.ItemID != item.ID {
		return domain.BacklogPlan{}, apierr.NotFound("PLAN_NOT_FOUND", fmt.Sprintf("%s has no plan %s", item.Ref(), planID))
	}
	return plan, nil
}

// ProjectPlan is a plan with the item it belongs to.
type ProjectPlan struct {
	ItemRef   string
	ItemTitle string
	Plan      domain.BacklogPlan
}

// ListOpen returns the project's plans that wait on someone (open, or failed
// and not planned again), each with its item's ref and title.
func (p *PlanService) ListOpen(ctx context.Context, projectID domain.ProjectID) ([]ProjectPlan, error) {
	if err := p.guard(); err != nil {
		return nil, err
	}
	plans, err := p.store.OpenBacklogPlansForProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := []ProjectPlan{}
	for _, plan := range plans {
		item, ok, err := p.bl.store.GetBacklogItem(ctx, plan.ItemID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		cur, ok, err := p.store.CurrentBacklogPlanForItem(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		if !ok || cur.ID != plan.ID {
			continue // a failed plan the owner already planned again
		}
		out = append(out, ProjectPlan{ItemRef: item.Ref(), ItemTitle: item.Title, Plan: plan})
	}
	return out, nil
}

// Start begins a plan for an item ("Plan now"): it records a plan, starts a
// planner session and links it to the item. An item with an open plan returns
// that plan; created is false then.
func (p *PlanService) Start(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string) (plan domain.BacklogPlan, created bool, err error) {
	if err := p.guard(); err != nil {
		return domain.BacklogPlan{}, false, err
	}
	if !decider(actor) {
		return domain.BacklogPlan{}, false, notDecider()
	}
	item, err := p.bl.resolve(ctx, projectID, ref)
	if err != nil {
		return domain.BacklogPlan{}, false, err
	}
	if item.State != domain.BacklogOpen {
		return domain.BacklogPlan{}, false, apierr.Conflict("BACKLOG_ITEM_NOT_OPEN", fmt.Sprintf("%s is %s: only open items can be planned", item.Ref(), item.State), nil)
	}
	if cur, ok, err := p.store.CurrentBacklogPlanForItem(ctx, item.ID); err != nil {
		return domain.BacklogPlan{}, false, err
	} else if ok && cur.Status == domain.PlanApproved {
		return domain.BacklogPlan{}, false, apierr.Conflict("PLAN_ALREADY_APPROVED", fmt.Sprintf("%s already has an approved plan: run `tallpine plan replan %s` to plan again", item.Ref(), item.Ref()), nil)
	} else if ok && cur.Status == domain.PlanFailed && cur.Answers != nil {
		// The owner already answered: the new planner gets the record and does not ask again.
		return p.startFor(ctx, item, actor, answerRecord(cur), p.supersededBrief(ctx, item, "", maxPlanContextRunes))
	}
	return p.startFor(ctx, item, actor, "", p.supersededBrief(ctx, item, "", maxPlanContextRunes))
}

// startFor creates the record and the planner session. record carries the
// questions and answers so far into a respawned planner's brief.
func (p *PlanService) startFor(ctx context.Context, item domain.BacklogItem, actor Actor, record string, extra ...string) (domain.BacklogPlan, bool, error) {
	now := p.now().UTC()
	commit, branch := "", ""
	if p.base != nil {
		var err error
		if commit, branch, err = p.base.Base(ctx, item.ProjectID); err != nil {
			// The stale check is advisory: a plan without a base commit still works.
			p.log.Warn("plan base commit unavailable", "item", item.Ref(), "error", err)
		}
	}
	plan, created, err := p.store.CreateBacklogPlan(ctx, domain.BacklogPlan{
		ID: p.newID(), ProjectID: item.ProjectID, ItemID: item.ID, Status: domain.PlanDrafting,
		BaseCommit: commit, BaseBranch: branch, ItemUpdatedAt: item.UpdatedAt, StartedAt: now, UpdatedAt: now,
		NotifySessionID: notifyTarget(actor),
	})
	if err != nil || !created {
		return plan, false, err
	}
	sid, err := p.sessions.SpawnPlanner(ctx, PlannerSpawn{
		ProjectID: item.ProjectID, ItemID: item.ID, ItemRef: item.Ref(), PlanID: plan.ID,
		DisplayName: "Plan " + item.Ref(), Prompt: plannerTask(item, record, extra...),
	})
	if err != nil {
		p.failPlan(ctx, plan.ID, "planner session did not start: "+err.Error())
		return domain.BacklogPlan{}, false, err
	}
	plan, err = p.store.UpdateBacklogPlan(ctx, plan.ID, func(pl *domain.BacklogPlan) error {
		pl.PlannerSessionID = sid
		pl.UpdatedAt = p.now().UTC()
		return nil
	})
	if err != nil {
		return domain.BacklogPlan{}, false, err
	}
	by := "human"
	if actor.SessionID != "" {
		by = actor.SessionID
	}
	if err := p.bl.LinkSession(ctx, item.ID, sid, by); err != nil {
		p.log.Warn("link planner session to item", "item", item.Ref(), "session", sid, "error", err)
	}
	p.changed(ctx, plan)
	return plan, true, nil
}

func (p *PlanService) failPlan(ctx context.Context, id, reason string) {
	failed, err := p.store.UpdateBacklogPlan(ctx, id, func(pl *domain.BacklogPlan) error {
		if !pl.Status.CanTransition(domain.PlanFailed) {
			return errNoChange
		}
		pl.Status, pl.Failure, pl.UpdatedAt = domain.PlanFailed, reason, p.now().UTC()
		return nil
	})
	if err != nil {
		if !errors.Is(err, errNoChange) {
			p.log.Warn("mark plan failed", "plan", id, "error", err)
		}
		return
	}
	p.changed(ctx, failed)
}

// plannerTask is the planner's task prompt: the item, the command to use, and
// on a respawn the questions and answers the owner already gave.
func plannerTask(item domain.BacklogItem, record string, extra ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan backlog item %s. Use `tallpine plan questions %s` and `tallpine plan submit %s` as your standing instructions say.\n\n", item.Ref(), item.Ref(), item.Ref())
	b.WriteString(item.TaskContext())
	if strings.TrimSpace(record) != "" {
		b.WriteString("\n\nThe owner already answered; do not ask again. Questions and answers so far:\n")
		b.WriteString(record)
	}
	for _, e := range extra {
		if e = strings.TrimSpace(e); e != "" {
			b.WriteString("\n\n" + e)
		}
	}
	return b.String()
}

// transition moves a plan to a new status under the state machine and returns
// the updated plan; mutate edits the other fields in the same write.
func (p *PlanService) transition(ctx context.Context, id string, to domain.PlanStatus, mutate func(*domain.BacklogPlan) error) (domain.BacklogPlan, error) {
	plan, err := p.store.UpdateBacklogPlan(ctx, id, func(pl *domain.BacklogPlan) error {
		if !pl.Status.CanTransition(to) {
			return apierr.Conflict("PLAN_WRONG_STATE", fmt.Sprintf("the plan is %s: it cannot become %s", pl.Status, to), nil)
		}
		if mutate != nil {
			if err := mutate(pl); err != nil {
				return err
			}
		}
		pl.Status, pl.UpdatedAt = to, p.now().UTC()
		return nil
	})
	if err == nil {
		p.changed(ctx, plan)
	}
	return plan, err
}

// notifyOrchestrator tells the project's orchestrator about a plan transition.
// A missing orchestrator or a failed delivery never fails the transition.
func (p *PlanService) notifyOrchestrator(ctx context.Context, plan domain.BacklogPlan, text string) {
	if p.notify == nil {
		return
	}
	if err := p.notify.NotifyPlanOrchestrator(ctx, plan.ProjectID, plan.NotifySessionID, text); err != nil && !errors.Is(err, ErrNoOrchestrator) {
		p.log.Warn("plan notification not delivered", "plan", plan.ID, "error", err)
	}
}

// alertOwner tells the human a plan waits on them; never fails the transition.
func (p *PlanService) alertOwner(ctx context.Context, plan domain.BacklogPlan) {
	if p.notifyOwner == nil {
		return
	}
	ref, title := plan.ItemID, ""
	if it, ok, err := p.bl.store.GetBacklogItem(ctx, plan.ItemID); err == nil && ok {
		ref, title = it.Ref(), it.Title
	}
	p.notifyOwner(ctx, plan, ref, title)
}

func (p *PlanService) itemRef(ctx context.Context, plan domain.BacklogPlan) string {
	if it, ok, err := p.bl.store.GetBacklogItem(ctx, plan.ItemID); err == nil && ok {
		return it.Ref()
	}
	return plan.ItemID
}

// stopPlanner terminates a superseded plan's planner so it stops burning a
// worker slot. Best effort.
func (p *PlanService) stopPlanner(ctx context.Context, plan domain.BacklogPlan) {
	if plan.PlannerSessionID == "" || p.isInPlace(ctx, plan) {
		return // an in-place implementer is a worker: closing or replanning never kills it
	}
	if err := p.sessions.TerminatePlanner(ctx, plan.PlannerSessionID); err != nil {
		p.log.Warn("stop superseded planner", "plan", plan.ID, "session", plan.PlannerSessionID, "error", err)
	}
}
