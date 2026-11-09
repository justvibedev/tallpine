package backlog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// PlanQuestionsInput is what a planner submits first: at most 5 questions with
// recommended answers and at most 20 one-line assumptions.
type PlanQuestionsInput struct {
	Questions   []domain.PlanQuestion
	Assumptions []domain.PlanAssumption
}

// PlanAnswerInput carries the owner's answers and assumption corrections.
type PlanAnswerInput struct {
	Questions   map[string]domain.PlanAnswer
	Corrections map[string]string
	// Relayed says an orchestrator passes on what the owner typed in chat. It is
	// forced on for an orchestrator actor and off for the owner.
	Relayed bool
}

// PlanSubmitInput is the finished plan.
type PlanSubmitInput struct {
	Plan      string
	Decisions string
	PRs       []domain.PlanPR
	Focus     []string
}

// PlanApproveInput identifies the owner message an orchestrator approves on.
type PlanApproveInput struct {
	// OwnerMessage is a chat message id of the owner, or "last" for the
	// owner's newest message in the project conversation. Required for an
	// orchestrator, ignored for the owner.
	OwnerMessage string
}

// errNoChange aborts a plan update that another writer made moot.
var errNoChange = errors.New("plan unchanged")

func invalidPlan(err error) error {
	return apierr.Invalid("PLAN_INVALID", err.Error(), nil)
}

// plannerPlan returns the item's open plan after checking that the actor is
// the planner session recorded on it: nobody else writes questions or a plan.
func (p *PlanService) plannerPlan(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string) (domain.BacklogPlan, error) {
	if err := p.guard(); err != nil {
		return domain.BacklogPlan{}, err
	}
	item, err := p.bl.resolve(ctx, projectID, ref)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	plan, ok, err := p.store.OpenBacklogPlanForItem(ctx, item.ID)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if !ok {
		return domain.BacklogPlan{}, apierr.Conflict("PLAN_NOT_OPEN", fmt.Sprintf("%s has no open plan", item.Ref()), nil)
	}
	if actor.Kind != domain.BacklogOriginWorker || actor.SessionID == "" || plan.PlannerSessionID == "" || actor.SessionID != string(plan.PlannerSessionID) {
		return domain.BacklogPlan{}, apierr.Forbidden("PLAN_NOT_PLANNER", "Only the planner session recorded on this plan can submit to it")
	}
	if p.isInPlace(ctx, plan) {
		if err := p.authorHasPR(ctx, plan); err != nil {
			return domain.BacklogPlan{}, err
		}
	}
	return plan, nil
}

// SubmitQuestions records the planner's questions and assumptions. With
// neither, the plan goes straight to writing; else it waits for the owner.
func (p *PlanService) SubmitQuestions(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string, in PlanQuestionsInput) (domain.BacklogPlan, error) {
	plan, err := p.plannerPlan(ctx, projectID, actor, ref)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if err := domain.ValidatePlanQuestions(in.Questions, in.Assumptions); err != nil {
		return domain.BacklogPlan{}, invalidPlan(err)
	}
	to := domain.PlanAwaitingAnswers
	if len(in.Questions) == 0 && len(in.Assumptions) == 0 {
		to = domain.PlanWriting
	}
	plan, err = p.transition(ctx, plan.ID, to, func(pl *domain.BacklogPlan) error {
		pl.Questions, pl.Assumptions = in.Questions, in.Assumptions
		return nil
	})
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if to == domain.PlanAwaitingAnswers {
		p.alertOwner(ctx, plan)
		p.notifyOrchestrator(ctx, plan, fmt.Sprintf("Plan for %s has %d question(s) and %d assumption(s) for the owner. The owner answers on the plan card, or you relay what they type with `tallpine plan answer %s --relayed` (see `tallpine plan show %s`).",
			p.itemRef(ctx, plan), len(plan.Questions), len(plan.Assumptions), p.itemRef(ctx, plan), p.itemRef(ctx, plan)))
	}
	return plan, nil
}

// Answer records the owner's answers and sends them to the planner.
func (p *PlanService) Answer(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string, in PlanAnswerInput) (domain.BacklogPlan, error) {
	return p.answer(ctx, projectID, actor, ref, &in)
}

// Defaults accepts every recommended answer and every assumption.
func (p *PlanService) Defaults(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string, relayed bool) (domain.BacklogPlan, error) {
	return p.answer(ctx, projectID, actor, ref, &PlanAnswerInput{Relayed: relayed})
}

func (p *PlanService) answer(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string, in *PlanAnswerInput) (domain.BacklogPlan, error) {
	if err := p.guard(); err != nil {
		return domain.BacklogPlan{}, err
	}
	if !decider(actor) {
		return domain.BacklogPlan{}, notDecider()
	}
	item, err := p.bl.resolve(ctx, projectID, ref)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	open, ok, err := p.store.OpenBacklogPlanForItem(ctx, item.ID)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if !ok {
		return domain.BacklogPlan{}, apierr.Conflict("PLAN_NOT_OPEN", fmt.Sprintf("%s has no open plan", item.Ref()), nil)
	}
	defaults := in.Questions == nil && in.Corrections == nil
	// An ended planner is dropped from the record in the same write that moves
	// the plan to writing, so a lifecycle sweep cannot fail the plan while a
	// replacement is starting.
	inPlace := p.isInPlace(ctx, open) // before a dead author is dropped from the record
	live := open.PlannerSessionID != "" && p.sessions.PlannerLive(ctx, open.PlannerSessionID)
	plan, err := p.transition(ctx, open.ID, domain.PlanWriting, func(pl *domain.BacklogPlan) error {
		if pl.Status != domain.PlanAwaitingAnswers {
			return apierr.Conflict("PLAN_WRONG_STATE", fmt.Sprintf("the plan is %s: it takes answers only while awaiting_answers", pl.Status), nil)
		}
		answers := domain.PlanAnswers{Questions: map[string]domain.PlanAnswer{}, Corrections: in.Corrections, Defaults: defaults, At: p.now().UTC(), By: "user"}
		if actor.Kind == domain.BacklogOriginOrchestrator {
			answers.By, answers.Relayed = "orchestrator:"+actor.SessionID, true
		}
		if defaults {
			for _, q := range pl.Questions {
				answers.Questions[q.ID] = domain.PlanAnswer{Option: q.Recommended}
			}
			answers.Corrections = nil
		} else if err := checkAnswers(pl, in, &answers); err != nil {
			return err
		}
		pl.Answers = &answers
		if !live {
			pl.PlannerSessionID = ""
		}
		return nil
	})
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	_, err = p.deliverAnswers(ctx, plan, item, inPlace)
	if err != nil {
		p.failPlan(ctx, plan.ID, "answers could not reach a planner: "+err.Error())
		return domain.BacklogPlan{}, err
	}
	return plan, nil
}

func checkAnswers(pl *domain.BacklogPlan, in *PlanAnswerInput, out *domain.PlanAnswers) error {
	for _, q := range pl.Questions {
		a, ok := in.Questions[q.ID]
		if !ok {
			return invalidPlan(fmt.Errorf("question %s has no answer", q.ID))
		}
		switch {
		case a.Other != "":
			if utf8.RuneCountInString(a.Other) > domain.MaxPlanAnswerChars {
				return invalidPlan(fmt.Errorf("the answer to %s is over %d characters", q.ID, domain.MaxPlanAnswerChars))
			}
		case !hasOption(q, a.Option):
			return invalidPlan(fmt.Errorf("question %s has no option %q", q.ID, a.Option))
		}
		out.Questions[q.ID] = a
	}
	for id := range in.Questions {
		if !hasQuestion(pl, id) {
			return invalidPlan(fmt.Errorf("unknown question %q", id))
		}
	}
	for id, text := range in.Corrections {
		if !hasAssumption(pl, id) {
			return invalidPlan(fmt.Errorf("unknown assumption %q", id))
		}
		if utf8.RuneCountInString(text) > domain.MaxPlanAnswerChars {
			return invalidPlan(fmt.Errorf("the correction to %s is over %d characters", id, domain.MaxPlanAnswerChars))
		}
	}
	return nil
}

func hasOption(q domain.PlanQuestion, id string) bool {
	for _, o := range q.Options {
		if o.ID == id {
			return true
		}
	}
	return false
}

func hasQuestion(pl *domain.BacklogPlan, id string) bool {
	for _, q := range pl.Questions {
		if q.ID == id {
			return true
		}
	}
	return false
}

func hasAssumption(pl *domain.BacklogPlan, id string) bool {
	for _, a := range pl.Assumptions {
		if a.ID == id {
			return true
		}
	}
	return false
}

// answerRecord renders the questions and the answers as the text a planner reads.
func answerRecord(pl domain.BacklogPlan) string {
	var b strings.Builder
	for _, q := range pl.Questions {
		a := pl.Answers.Questions[q.ID]
		answer := a.Other
		if answer == "" {
			for _, o := range q.Options {
				if o.ID == a.Option {
					answer = o.Label
				}
			}
		}
		fmt.Fprintf(&b, "- Q %s: %s\n  Answer: %s\n", q.ID, q.Text, answer)
	}
	for _, a := range pl.Assumptions {
		if c, ok := pl.Answers.Corrections[a.ID]; ok {
			fmt.Fprintf(&b, "- Assumption %s: %s\n  Correction: %s\n", a.ID, a.Text, c)
		} else {
			fmt.Fprintf(&b, "- Assumption %s: %s\n  Accepted.\n", a.ID, a.Text)
		}
	}
	return b.String()
}

// deliverAnswers sends the answers to the plan's planner. When that session
// has ended, a new planner is started with the record in its brief. It reports
// whether it started a new one.
func (p *PlanService) deliverAnswers(ctx context.Context, plan domain.BacklogPlan, item domain.BacklogItem, inPlace bool) (bool, error) {
	record := answerRecord(plan)
	msg := fmt.Sprintf("The owner answered. Write and submit the plan now with `tallpine plan submit %s`.\n\n%s", item.Ref(), record)
	if plan.PlannerSessionID != "" {
		delivered, err := p.sessions.SendToPlanner(ctx, plan.PlannerSessionID, msg)
		if err == nil && delivered {
			return false, nil
		}
		if err != nil {
			p.log.Warn("send answers to planner", "plan", plan.ID, "session", plan.PlannerSessionID, "error", err)
		}
	}
	spawn := PlannerSpawn{
		ProjectID: plan.ProjectID, ItemID: item.ID, ItemRef: item.Ref(), PlanID: plan.ID,
		DisplayName: "Plan " + item.Ref(), Prompt: plannerTask(item, record),
	}
	spawnFn := p.sessions.SpawnPlanner
	if inPlace {
		// The author ended: start the implementer again in place, never a planner.
		spawn.DisplayName, spawn.LaneItemID, spawn.Prompt = item.Ref(), item.ParentID, p.inPlaceTask(ctx, item, record)
		spawnFn = p.sessions.SpawnInPlace
	}
	sid, err := spawnFn(ctx, spawn)
	if err != nil {
		return false, err
	}
	if _, err := p.store.UpdateBacklogPlan(ctx, plan.ID, func(pl *domain.BacklogPlan) error {
		pl.PlannerSessionID, pl.UpdatedAt = sid, p.now().UTC()
		return nil
	}); err != nil {
		return true, err
	}
	if err := p.bl.LinkSession(ctx, item.ID, sid, "planner"); err != nil {
		p.log.Warn("link planner session to item", "item", item.Ref(), "session", sid, "error", err)
	}
	return true, nil
}

// Submit stores the finished plan and asks for approval.
func (p *PlanService) Submit(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string, in PlanSubmitInput) (domain.BacklogPlan, error) {
	plan, err := p.plannerPlan(ctx, projectID, actor, ref)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if err := domain.ValidatePlanSubmission(in.Plan, in.Decisions, in.PRs, in.Focus); err != nil {
		return domain.BacklogPlan{}, invalidPlan(err)
	}
	// Defence in depth: a planner is read-only and opens no PR.
	facts, err := p.bl.store.BacklogFactsFor(ctx, plan.ItemID)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if sessionHasPR(facts, string(plan.PlannerSessionID), "merged", "open", "draft", "closed") {
		return domain.BacklogPlan{}, apierr.Conflict("PLAN_PLANNER_HAS_PR", "The planner session has a pull request: a planner is read-only", nil)
	}
	plan, err = p.storeSubmission(ctx, plan, in)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if p.autoApproveOn(ctx, projectID) && !decisionsNeedOwner(plan.DecisionsMD) {
		// The project opted into unattended approval (a human's setting). Same
		// side effects as an approval by the owner; on failure the plan stays
		// submitted and the owner is asked as usual.
		approved, aerr := p.approve(ctx, projectID, Actor{Kind: domain.BacklogOriginHuman}, ref, PlanApproveInput{}, AutoApprovedBy)
		if aerr == nil {
			return approved, nil
		}
		p.log.Warn("plan auto-approve failed; waiting for the owner", "plan", plan.ID, "err", aerr)
	}
	p.alertOwner(ctx, plan)
	ir := p.itemRef(ctx, plan)
	p.notifyOrchestrator(ctx, plan, fmt.Sprintf("Plan for %s is ready for the owner to approve (`tallpine plan show %s`). Do not approve without the owner's word.", ir, ir))
	return plan, nil
}

// verifyOwnerMessage checks that an orchestrator's approval rests on a message
// the owner typed into the project conversation after the plan last changed.
func (p *PlanService) verifyOwnerMessage(ctx context.Context, plan domain.BacklogPlan, id string) (string, error) {
	// An eval project has no owner chat: its orchestrator is the owner's proxy.
	// Only a creating orchestrator reaches the plan routes of such a project
	// (the scope gate), and a real project keeps the guard below.
	if p.eval != nil && p.eval(ctx, plan.ProjectID) {
		return "", nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "", apierr.Forbidden("PLAN_OWNER_MESSAGE_REQUIRED", "An orchestrator needs --owner-message: the owner's chat message that approves; never approve on your own")
	}
	if id == "last" {
		mid, at, ok, err := p.store.LatestHumanProjectMessage(ctx, plan.ProjectID)
		if err != nil {
			return "", err
		}
		if !ok || !at.After(plan.UpdatedAt) {
			return "", apierr.Forbidden("PLAN_OWNER_MESSAGE_STALE", "The owner has not written since the plan last changed: ask them first")
		}
		return mid, nil
	}
	src, ok, err := p.store.BacklogPlanMessageSource(ctx, id)
	if err != nil {
		return "", err
	}
	switch {
	case !ok:
		return "", apierr.Forbidden("PLAN_OWNER_MESSAGE_INVALID", "That message does not exist")
	case !src.Human || src.ProjectID != plan.ProjectID:
		return "", apierr.Forbidden("PLAN_OWNER_MESSAGE_INVALID", "That message is not the owner's message in this project")
	case !src.At.After(plan.UpdatedAt):
		return "", apierr.Forbidden("PLAN_OWNER_MESSAGE_STALE", "That message is older than the plan's last change: ask the owner again")
	}
	return id, nil
}

// Approve approves a submitted plan. The owner approves directly; an
// orchestrator needs the owner's message.
func (p *PlanService) Approve(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string, in PlanApproveInput) (domain.BacklogPlan, error) {
	if err := p.guard(); err != nil {
		return domain.BacklogPlan{}, err
	}
	if !decider(actor) {
		return domain.BacklogPlan{}, notDecider()
	}
	return p.approve(ctx, projectID, actor, ref, in, "user")
}

// AutoApprovedBy is the ApprovedBy of a plan approved by the project's
// planAutoApprove setting.
const AutoApprovedBy = "auto (project setting)"

// autoApproveOn reports whether the project approves submitted plans itself.
func (p *PlanService) autoApproveOn(ctx context.Context, projectID domain.ProjectID) bool {
	return p.autoApprove != nil && p.autoApprove(ctx, projectID)
}

// approve is Approve after the actor check; approvedBy is recorded on the plan.
func (p *PlanService) approve(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string, in PlanApproveInput, approvedBy string) (domain.BacklogPlan, error) {
	item, err := p.bl.resolve(ctx, projectID, ref)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	open, ok, err := p.store.OpenBacklogPlanForItem(ctx, item.ID)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if !ok {
		return domain.BacklogPlan{}, apierr.Conflict("PLAN_NOT_OPEN", fmt.Sprintf("%s has no open plan", item.Ref()), nil)
	}
	by, msgID := approvedBy, ""
	// Children are made before the approval write and deleted if it fails, so a
	// lost race or a failed write leaves no orphans.
	var children []string
	// A re-plan settles the children an earlier plan made: unstarted ones are
	// dropped after the approval lands, started or done ones stay, and the new
	// split's children are made here (undone if the approval write fails).
	old, err := p.bl.planMadeChildren(ctx, item)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if open.Status == domain.PlanAwaitingApproval && item.ParentID == "" {
		// Refuse before creating anything when the approval itself cannot land.
		if actor.Kind == domain.BacklogOriginOrchestrator {
			if _, err := p.verifyOwnerMessage(ctx, open, in.OwnerMessage); err != nil {
				return domain.BacklogPlan{}, err
			}
		}
		minPRs := 2
		if countSurvivors(old) > 0 {
			minPRs = 1 // the remaining work may be one PR, but the item stays a parent
		}
		if children, err = p.bl.createPlanChildren(ctx, item, open.PRSplit, actor, minPRs); err != nil {
			return domain.BacklogPlan{}, err
		}
	}
	plan, err := p.transition(ctx, open.ID, domain.PlanApproved, func(pl *domain.BacklogPlan) error {
		if pl.Status != domain.PlanAwaitingApproval {
			return apierr.Conflict("PLAN_WRONG_STATE", fmt.Sprintf("the plan is %s: only a submitted plan can be approved", pl.Status), nil)
		}
		if actor.Kind == domain.BacklogOriginOrchestrator {
			id, err := p.verifyOwnerMessage(ctx, *pl, in.OwnerMessage)
			if err != nil {
				return err
			}
			by, msgID = "orchestrator:"+actor.SessionID, id
		}
		pl.ApprovedBy, pl.OwnerMessageID, pl.ApprovedAt = by, msgID, p.now().UTC()
		return nil
	})
	if err != nil {
		p.bl.undoPlanChildren(ctx, item, children)
		return domain.BacklogPlan{}, err
	}
	p.bl.retirePlanChildren(ctx, item, old, len(children) == 0 && countSurvivors(old) == 0)
	if p.isInPlace(ctx, plan) {
		p.approveInPlace(ctx, plan, item, children)
		return plan, nil
	}
	next := fmt.Sprintf("`tallpine spawn --backlog %s`", item.Ref())
	if len(children) > 0 {
		next = fmt.Sprintf("`tallpine spawn --backlog <ref>` for the first of the %d child items of %s now, and for each next one once the one before it is done (each is blocked by the one before; a blocked spawn is refused)", len(children), item.Ref())
	}
	p.notifyOrchestrator(ctx, plan, fmt.Sprintf("Plan for %s is approved. Spawn the implementer with %s: it receives the decisions and its PR entry.", item.Ref(), next))
	return plan, nil
}

// Replan supersedes the item's plan and starts a new one.
func (p *PlanService) Replan(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string) (domain.BacklogPlan, error) {
	if err := p.guard(); err != nil {
		return domain.BacklogPlan{}, err
	}
	if !decider(actor) {
		return domain.BacklogPlan{}, notDecider()
	}
	item, err := p.bl.resolve(ctx, projectID, ref)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if item.State != domain.BacklogOpen {
		return domain.BacklogPlan{}, apierr.Conflict("BACKLOG_ITEM_NOT_OPEN", fmt.Sprintf("%s is %s: only open items can be planned", item.Ref(), item.State), nil)
	}
	cur, hasCur, err := p.store.CurrentBacklogPlanForItem(ctx, item.ID)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if hasCur && p.isInPlace(ctx, cur) {
		return p.replanInPlace(ctx, cur, item, actor)
	}
	// An open plan blocks the new record (one open plan per item), so it is
	// superseded first. An approved or failed plan stays in effect until the
	// replacement's planner has started: a spawn failure must not lose approval.
	if hasCur && cur.Status.Open() {
		if _, err := p.transition(ctx, cur.ID, domain.PlanSuperseded, nil); err != nil {
			return domain.BacklogPlan{}, err
		}
		p.stopPlanner(ctx, cur)
	}
	plan, _, err := p.startFor(ctx, item, actor, "", p.bl.planChildrenBrief(ctx, item))
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if hasCur && !cur.Status.Open() {
		if _, err := p.transition(ctx, cur.ID, domain.PlanSuperseded, nil); err != nil {
			return domain.BacklogPlan{}, err
		}
		p.stopPlanner(ctx, cur)
	}
	return plan, nil
}

// BuildAnyway records that the owner builds from an approved plan although it
// may be stale. The stale check itself lands with the backlog item surface.
func (p *PlanService) BuildAnyway(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string, in PlanApproveInput) (domain.BacklogPlan, error) {
	if err := p.guard(); err != nil {
		return domain.BacklogPlan{}, err
	}
	if !decider(actor) {
		return domain.BacklogPlan{}, notDecider()
	}
	item, err := p.bl.resolve(ctx, projectID, ref)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	cur, ok, err := p.store.CurrentBacklogPlanForItem(ctx, item.ID)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if !ok || cur.Status != domain.PlanApproved {
		return domain.BacklogPlan{}, apierr.Conflict("PLAN_WRONG_STATE", fmt.Sprintf("%s has no approved plan", item.Ref()), nil)
	}
	return p.store.UpdateBacklogPlan(ctx, cur.ID, func(pl *domain.BacklogPlan) error {
		if actor.Kind == domain.BacklogOriginOrchestrator {
			if _, err := p.verifyOwnerMessage(ctx, *pl, in.OwnerMessage); err != nil {
				return err
			}
		}
		pl.BuildAnyway, pl.UpdatedAt = true, p.now().UTC()
		return nil
	})
}
