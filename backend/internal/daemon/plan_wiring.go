package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/plannerpolicy"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/gitdefault"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/process"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// newPlanService builds the plan service behind the TALLPINE_PLANNER switch;
// with the switch off every call answers "disabled".
func newPlanService(cfg config.Config, store *sqlite.Store, backlog *backlogsvc.Service, sessions planSessions, policy *agentpolicy.Engine, owner planOwner) *backlogsvc.PlanService {
	return backlogsvc.NewPlanService(backlogsvc.PlanDeps{
		Backlog: backlog, Store: store, Sessions: plannerSessions{sessions: sessions},
		Notify: newBacklogNotifier(sessions, store), Base: planBase{store: store, resolver: gitdefault.New("", nil)}, Diff: planBase{store: store, resolver: gitdefault.New("", nil)},
		Mode:        planModeSource{store: store, policy: policy}.Mode,
		AutoApprove: planModeSource{store: store, policy: policy}.AutoApprove,
		InPlace:     planModeSource{store: store, policy: policy}.InPlace,
		Eval:        planModeSource{store: store, policy: policy}.Eval,
		Enabled:     cfg.Planner,
		NotifyOwner: planOwnerAlert(owner, sessions, store),
		ClearOwner:  planOwnerClear(owner),
		LaneOf:      planLaneOf(sessions),
	})
}

// planModeSource resolves a project's planning mode: the managed policy lock,
// else the project's planningMode setting, else auto. Plan-first refusal
// (always) and backlog runs read it, so the setting takes effect in production.
type planModeSource struct {
	store  *sqlite.Store
	policy *agentpolicy.Engine
}

func (m planModeSource) Mode(ctx context.Context, projectID domain.ProjectID) domain.PlanningMode {
	var cfg domain.ProjectConfig
	if rec, ok, err := m.store.GetProject(ctx, string(projectID)); err == nil && ok {
		cfg = rec.Config
	}
	var lock plannerpolicy.Outcome
	if m.policy != nil {
		lock = plannerpolicy.Of(m.policy.DecideStatic(ctx))
	}
	return plannerpolicy.ResolvePlanningMode(lock, cfg, m.defaults(ctx))
}

// defaults reads the app-wide workflow defaults; a failed read gives the
// built-in ones (auto-approve off).
func (m planModeSource) defaults(ctx context.Context) domain.WorkflowDefaults {
	d, err := m.store.WorkflowDefaults(ctx)
	if err != nil {
		return domain.DefaultWorkflowDefaults()
	}
	return d.Normalized()
}

// AutoApprove reports whether the project approves plans itself: its own
// planAutoApprove, else the app default, unless a managed policy locks it off.
// A failed project read or a missing policy engine means off: auto-approval
// never opens by accident.
func (m planModeSource) AutoApprove(ctx context.Context, projectID domain.ProjectID) bool {
	if m.policy == nil || plannerpolicy.Of(m.policy.DecideStatic(ctx)).AutoApproveLocked {
		return false
	}
	rec, ok, err := m.store.GetProject(ctx, string(projectID))
	return err == nil && ok && rec.Config.EffectivePlanAutoApprove(m.defaults(ctx))
}

// InPlace reports whether the project's planInPlace setting is on and no
// managed policy locks it off. Like AutoApprove, a failed read means off.
func (m planModeSource) InPlace(ctx context.Context, projectID domain.ProjectID) bool {
	if m.policy == nil || plannerpolicy.Of(m.policy.DecideStatic(ctx)).InPlaceLocked {
		return false
	}
	rec, ok, err := m.store.GetProject(ctx, string(projectID))
	return err == nil && ok && rec.Config.PlanInPlace
}

// Eval reports whether the project is marked eval and no managed policy locks
// plan auto-approval off: the eval waiver lets an orchestrator approve without
// the owner's message, which is what that lock forbids in every project. A
// failed read or a missing policy engine means no, so the guard stays on.
func (m planModeSource) Eval(ctx context.Context, projectID domain.ProjectID) bool {
	if m.policy == nil || plannerpolicy.Of(m.policy.DecideStatic(ctx)).AutoApproveLocked {
		return false
	}
	if d, err := m.store.WorkflowDefaults(ctx); err != nil || d.EvalWaiverClosed {
		return false
	}
	rec, ok, err := m.store.GetProject(ctx, string(projectID))
	return err == nil && ok && rec.Config.Eval
}

type planSessions interface {
	backlogRunSessions
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error)
	Kill(ctx context.Context, id domain.SessionID) (bool, error)
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
}

// planLeadItems reads the backlog item behind a plan and its lane parent.
type planLeadItems interface {
	GetBacklogItem(ctx context.Context, id string) (domain.BacklogItem, bool, error)
}

// plannerSessions starts planner workers and messages them through the
// session service.
type plannerSessions struct{ sessions planSessions }

func (p plannerSessions) SpawnPlanner(ctx context.Context, in backlogsvc.PlannerSpawn) (domain.SessionID, error) {
	cfg := ports.SpawnConfig{
		ProjectID: in.ProjectID, Kind: domain.KindWorker, Role: domain.SessionRolePlanner,
		RequestedOutput: domain.RequestedOutputArtifact, Prompt: in.Prompt, DisplayName: in.DisplayName,
		ParentSessionID: in.StarterSessionID,
	}
	sess, _, _, err := p.sessions.Spawn(ctx, cfg)
	if errors.Is(err, sessionmanager.ErrMissingHarness) {
		// No planner, orchestrator or worker override names a harness: inherit
		// the live orchestrator's harness and model.
		active := true
		found, listErr := p.sessions.List(ctx, sessionsvc.ListFilter{ProjectID: in.ProjectID, Active: &active, OrchestratorOnly: true})
		if listErr != nil || len(found) == 0 {
			return "", err
		}
		cfg.Harness = found[0].Harness
		cfg.AgentConfig.Model = found[0].Metadata.Model
		sess, _, _, err = p.sessions.Spawn(ctx, cfg)
	}
	if err != nil {
		return "", err
	}
	return sess.ID, nil
}

// SpawnInPlace starts an ordinary implementer (role worker) that authors the
// plan itself; the record is in its brief when the first one ended (B335).
func (p plannerSessions) SpawnInPlace(ctx context.Context, in backlogsvc.PlannerSpawn) (domain.SessionID, error) {
	sess, _, _, err := p.sessions.Spawn(ctx, ports.SpawnConfig{
		ProjectID: in.ProjectID, Kind: domain.KindWorker, LaneItemID: in.LaneItemID, ParentSessionID: in.StarterSessionID, Prompt: "Continue backlog item " + in.ItemRef + ".", BacklogContext: in.Prompt, DisplayName: in.DisplayName,
	})
	if err != nil {
		return "", err
	}
	return sess.ID, nil
}

func (p plannerSessions) TerminatePlanner(ctx context.Context, id domain.SessionID) error {
	_, err := p.sessions.Kill(ctx, id)
	return err
}

func (p plannerSessions) PlannerLive(ctx context.Context, id domain.SessionID) bool {
	sess, err := p.sessions.Get(ctx, id)
	return err == nil && !sess.IsTerminated
}

func (p plannerSessions) SendToPlanner(ctx context.Context, id domain.SessionID, message string) (bool, error) {
	sess, err := p.sessions.Get(ctx, id)
	if err != nil {
		return false, nil //nolint:nilerr // a session that cannot be read is gone: the caller starts a new planner
	}
	if sess.IsTerminated {
		return false, nil
	}
	if err := p.sessions.SendWithOptions(ctx, id, message, nil, ports.MessageDeliveryOptions{}); err != nil {
		return false, err
	}
	return true, nil
}

// planBase names the commit of the project's default branch a plan starts from.
type planBase struct {
	store    *sqlite.Store
	resolver *gitdefault.Resolver
}

func (b planBase) Base(ctx context.Context, projectID domain.ProjectID) (string, string, error) {
	project, ok, err := b.store.GetProject(ctx, string(projectID))
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", fmt.Errorf("project %s not found", projectID)
	}
	res, err := b.resolver.Inspect(ctx, project.Path)
	if err != nil {
		return "", "", err
	}
	out, err := process.CommandContext(ctx, "git", "-C", project.Path, "rev-parse", "--verify", res.Ref).Output()
	if err != nil {
		return "", res.Branch, err
	}
	return strings.TrimSpace(string(out)), res.Branch, nil
}

// ChangedSince lists the files that changed on the default branch since commit.
func (b planBase) ChangedSince(ctx context.Context, projectID domain.ProjectID, commit string) ([]string, error) {
	project, ok, err := b.store.GetProject(ctx, string(projectID))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("project %s not found", projectID)
	}
	res, err := b.resolver.Inspect(ctx, project.Path)
	if err != nil {
		return nil, err
	}
	return gitChangedFiles(ctx, project.Path, commit, res.Ref)
}

// ChangedLines returns the added and removed lines per path since commit.
func (b planBase) ChangedLines(ctx context.Context, projectID domain.ProjectID, commit string, paths []string) (map[string][]string, error) {
	project, ok, err := b.store.GetProject(ctx, string(projectID))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("project %s not found", projectID)
	}
	res, err := b.resolver.Inspect(ctx, project.Path)
	if err != nil {
		return nil, err
	}
	args := append([]string{"-C", project.Path, "diff", "-U0", "--no-renames", commit + ".." + res.Ref, "--"}, paths...)
	out, err := process.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return nil, err
	}
	return parseDiffLines(string(out)), nil
}

// parseDiffLines groups the +/- lines of a -U0 diff by file. A file added or
// deleted outright gets no entry, so callers treat it as touching.
func parseDiffLines(diff string) map[string][]string {
	res := map[string][]string{}
	var cur string
	skip := false
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			cur, skip = "", false
		case strings.HasPrefix(l, "new file mode"), strings.HasPrefix(l, "deleted file mode"), strings.HasPrefix(l, "Binary files"):
			skip = true
			delete(res, cur)
		case strings.HasPrefix(l, "+++ b/"):
			cur = strings.TrimPrefix(l, "+++ b/")
			if !skip {
				res[cur] = []string{}
			}
		case strings.HasPrefix(l, "@@") && cur != "" && !skip:
			// The text after the second @@ is the enclosing function line.
			if i := strings.Index(l[2:], "@@"); i >= 0 {
				res[cur] = append(res[cur], l[2+i+2:])
			}
		case strings.HasPrefix(l, "--- "), strings.HasPrefix(l, "+++ "):
		case (strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-")) && cur != "" && !skip:
			res[cur] = append(res[cur], l[1:])
		}
	}
	return res
}

// gitChangedFiles lists the files changed between two refs. Renames are listed
// as a delete plus an add (--no-renames), so a moved planned file still counts.
func gitChangedFiles(ctx context.Context, repo, from, to string) ([]string, error) {
	out, err := process.CommandContext(ctx, "git", "-C", repo, "diff", "--name-only", "--no-renames", from+".."+to).Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

// workerPlanSource finds the approved plan a worker builds from: the plan of
// the live backlog item linked to the worker, else of that item's parent
// (children made from a PR split share the parent's plan).
type workerPlanSource struct{ store *sqlite.Store }

func (w workerPlanSource) ApprovedPlanForWorker(ctx context.Context, id domain.SessionID) (string, []string, bool, error) {
	items, err := w.store.ListLiveBacklogItemsLinkedTo(ctx, domain.BacklogLinkSession, string(id))
	if err != nil {
		return "", nil, false, err
	}
	for _, it := range items {
		for _, itemID := range []string{it.ID, it.ParentID} {
			if itemID == "" {
				continue
			}
			plan, ok, err := w.store.CurrentBacklogPlanForItem(ctx, itemID)
			if err != nil {
				return "", nil, false, err
			}
			if ok && plan.Status == domain.PlanApproved {
				return plan.DecisionsMD, plan.ReviewFocus, true, nil
			}
		}
	}
	return "", nil, false, nil
}

// LinkedItemForWorker returns the live backlog item the worker was started on,
// read fresh so reviewers see edits made after the worker started. Items the
// worker itself filed (--defer, agent create) link with CreatedBy == the
// session and are not candidates; with zero or several candidates it reports
// none.
func (w workerPlanSource) LinkedItemForWorker(ctx context.Context, id domain.SessionID) (domain.BacklogItem, bool, error) {
	items, err := w.store.ListLiveBacklogItemsLinkedTo(ctx, domain.BacklogLinkSession, string(id))
	if err != nil {
		return domain.BacklogItem{}, false, err
	}
	var found []domain.BacklogItem
	for _, it := range items {
		full, ok, err := w.store.GetBacklogItem(ctx, it.ID)
		if err != nil {
			return domain.BacklogItem{}, false, err
		}
		if !ok {
			continue
		}
		for _, l := range full.Links {
			if l.Kind == domain.BacklogLinkSession && l.Ref == string(id) && l.CreatedBy != string(id) {
				found = append(found, full)
				break
			}
		}
	}
	if len(found) != 1 {
		return domain.BacklogItem{}, false, nil
	}
	return found[0], true, nil
}

// ownerNotifier writes a stored notification (and so the push) for the human.
type ownerNotifier interface {
	Notify(ctx context.Context, intent ports.NotificationIntent) error
}

// planOwner also resolves the plan's notification once its cause is gone.
type planOwner interface {
	ownerNotifier
	Resolve(ctx context.Context, res ports.NotificationResolution) error
}

// planOwnerClear resolves the planner's plan_waiting notification once the
// plan stops waiting on the owner, so an answered plan no longer reads as
// pending on desktop or phone. A failure is logged only.
func planOwnerClear(owner planOwner) func(ctx context.Context, plan domain.BacklogPlan) {
	if owner == nil {
		return nil
	}
	return func(ctx context.Context, plan domain.BacklogPlan) {
		// Scope the clear to this plan's alerts: a lane orchestrator's other waiting
		// plans share its session. The key is "plan-waiting[-lane]:<plan id>:<state>:...".
		for _, prefix := range []string{"plan-waiting:", domain.PlanLaneSourcePrefix} {
			if err := owner.Resolve(ctx, ports.NotificationResolution{Type: domain.NotificationPlanWaiting, SourceKeyPrefix: prefix + plan.ID + ":"}); err != nil {
				slog.Warn("could not clear a plan's owner notification", "plan", plan.ID, "error", err)
			}
		}
	}
}

// planOwnerAlert turns a plan reaching awaiting_answers or awaiting_approval
// into a plan_waiting notification. A nil notifier skips it; a failure is
// logged and never fails the plan transition.
func planOwnerAlert(owner ownerNotifier, sessions planLeadSessions, items planLeadItems) func(ctx context.Context, plan domain.BacklogPlan, itemRef, itemTitle string) {
	if owner == nil {
		return nil
	}
	lead := &planLeadNotice{sessions: sessions, items: items, sent: map[string]bool{}}
	return func(ctx context.Context, plan domain.BacklogPlan, itemRef, itemTitle string) {
		if plan.PlannerSessionID == "" {
			return
		}
		lead.notify(ctx, plan, itemRef)
		title, verb := fmt.Sprintf("Plan for %s needs your approval", itemRef), "approve"
		if plan.Status == domain.PlanAwaitingAnswers {
			title, verb = fmt.Sprintf("Plan for %s has questions for you", itemRef), "answer"
		}
		body := fmt.Sprintf("Open the plan card to %s it.", verb)
		if itemTitle != "" {
			body = itemTitle + "\n" + body
		}
		session, prefix := planAlertSession(ctx, sessions, plan), "plan-waiting:"
		if session != plan.PlannerSessionID {
			prefix = domain.PlanLaneSourcePrefix
		}
		if err := owner.Notify(ctx, ports.NotificationIntent{
			Type: domain.NotificationPlanWaiting, SessionID: session, ProjectID: plan.ProjectID,
			SourceKey: prefix + plan.ID + ":" + string(plan.Status) + ":" + plan.UpdatedAt.Format(time.RFC3339Nano),
			Title:     title, Body: body,
		}); err != nil {
			slog.Warn("could not notify the owner of a waiting plan", "plan", plan.ID, "error", err)
		}
	}
}

// planLaneOf names the lane a live lane orchestrator owns ("" for any other
// session), so a plan's card shows only in the chat of the lane that started it.
func planLaneOf(sessions planLeadSessions) func(ctx context.Context, id domain.SessionID) string {
	if sessions == nil {
		return nil
	}
	return func(ctx context.Context, id domain.SessionID) string {
		active := true
		found, err := sessions.List(ctx, sessionsvc.ListFilter{Active: &active, OrchestratorOnly: true})
		if err != nil {
			return ""
		}
		for _, s := range found {
			if s.ID == id {
				return s.Lane
			}
		}
		return ""
	}
}

// planAlertSession is the session a plan_waiting alert opens: the lane
// orchestrator that started the plan, else the planner (the project's main
// chat opens from the project). Alert and clear use the same session.
func planAlertSession(ctx context.Context, sessions planLeadSessions, plan domain.BacklogPlan) domain.SessionID {
	if sessions != nil && plan.NotifySessionID != "" {
		if lane := planLaneOf(sessions); lane != nil && lane(ctx, plan.NotifySessionID) != "" {
			return plan.NotifySessionID
		}
	}
	return plan.PlannerSessionID
}
