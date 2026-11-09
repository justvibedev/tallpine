package backlog

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// createPlanChildren turns an approved PR split of two or more entries into
// child items of the planned item: one per PR, titled with the PR, done-when
// from its tests line, each blocked by the one before. A one-PR plan makes no
// children. On a failure it deletes what it made and returns the error.
func (s *Service) createPlanChildren(ctx context.Context, parent domain.BacklogItem, split []domain.PlanPR, actor Actor, minPRs int) ([]string, error) {
	if len(split) < minPRs || len(split) == 0 {
		return nil, nil
	}
	var made []string
	undo := func(err error) ([]string, error) {
		s.undoPlanChildren(ctx, parent, made)
		return nil, err
	}
	prev := ""
	for i, pr := range split {
		now := s.now().UTC()
		child := domain.BacklogItem{
			ID: s.newID(), ProjectID: parent.ProjectID, ParentID: parent.ID, State: domain.BacklogOpen,
			Title: truncateRunes(strings.TrimSpace(pr.Title), domain.MaxBacklogTitle), Note: planChildNote(pr, parent.Ref(), i+1, len(split)),
			DoneWhen: truncateRunes(strings.TrimSpace(pr.Tests), domain.MaxBacklogDoneWhen), Scope: parent.Scope,
			Fields:    map[string]string{planPRField: strconv.Itoa(i + 1)},
			Origin:    domain.BacklogOrigin{Kind: actor.Kind, SessionID: actor.SessionID},
			CreatedAt: now, UpdatedAt: now,
		}
		if child.Title == "" {
			child.Title = fmt.Sprintf("%s part %d", parent.Title, i+1)
		}
		created, err := s.store.CreateBacklogItem(ctx, child)
		if err != nil {
			return undo(mapError(err))
		}
		made = append(made, created.ID)
		if prev != "" {
			if _, err := s.store.AddBacklogLink(ctx, created.ID, domain.BacklogLink{Kind: domain.BacklogLinkBlockedBy, Ref: prev, CreatedBy: "plan"}, now); err != nil {
				return undo(mapError(err))
			}
		}
		prev = created.ID
	}
	return made, nil
}

func planChildNote(pr domain.PlanPR, parentRef string, n, total int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PR %d of %d from the approved plan of %s (`tallpine plan show %s`).\n", n, total, parentRef, parentRef)
	if s := strings.TrimSpace(pr.Summary); s != "" {
		fmt.Fprintf(&b, "\n%s\n", s)
	}
	if len(pr.Files) > 0 {
		fmt.Fprintf(&b, "\nFiles: %s\n", strings.Join(pr.Files, ", "))
	}
	return truncateRunes(strings.TrimRight(b.String(), "\n"), domain.MaxBacklogNote)
}

// undoPlanChildren removes children made for a plan whose approval did not
// land. Only closed items can be deleted, so each is dropped first.
func (s *Service) undoPlanChildren(ctx context.Context, parent domain.BacklogItem, ids []string) {
	for _, id := range ids {
		if _, err := s.store.SetBacklogItemState(ctx, id, domain.BacklogDropped, "plan not approved", s.now().UTC()); err != nil {
			s.log.Warn("undo plan child", "item", id, "error", err)
			continue
		}
		if _, err := s.store.DeleteBacklogItem(ctx, id); err != nil {
			s.log.Warn("undo plan child", "item", id, "error", err)
		}
	}
	s.reopenAutoClosed(ctx, parent)
}

// planPRField is the child item field holding its 1-based entry in the
// parent's PR split. It is the stable key: titles can be edited or repeat.
const planPRField = "plan.pr"

// planEntry returns the child's PR-split entry index (0-based) in a split of n.
func planEntry(item domain.BacklogItem, n int) (int, bool) {
	v, err := strconv.Atoi(item.Fields[planPRField])
	if err != nil || v < 1 || v > n {
		return 0, false
	}
	return v - 1, true
}

// planMadeChildren lists the children an earlier plan made (they carry plan.pr).
func (s *Service) planMadeChildren(ctx context.Context, parent domain.BacklogItem) ([]domain.BacklogItem, error) {
	kids, err := s.store.ListBacklogItems(ctx, parent.ProjectID, store.BacklogFilter{ParentID: parent.ID, Limit: domain.MaxBacklogListLimit})
	if err != nil {
		return nil, err
	}
	var out []domain.BacklogItem
	for _, k := range kids {
		if _, ok := k.Fields[planPRField]; ok {
			out = append(out, s.withoutAbandonedWork(ctx, k))
		}
	}
	return out, nil
}

// withoutAbandonedWork treats a child whose every linked worker ended with no
// live PR as unstarted (only when its effective scope, its own or its open
// run's, expects a PR: a local child never has one), so a re-plan retires it instead of leaving a duplicate
// beside the new split (B451). Any PR link, live session or lookup failure keeps it started.
func (s *Service) withoutAbandonedWork(ctx context.Context, k domain.BacklogItem) domain.BacklogItem {
	if k.State != domain.BacklogOpen || k.LinkCounts[domain.BacklogLinkPR] > 0 || k.LinkCounts[domain.BacklogLinkSession] == 0 {
		return k
	}
	if sc := s.effectiveScope(ctx, k); sc != domain.BacklogScopePR && sc != domain.BacklogScopePRMerge {
		return k
	}
	facts, err := s.store.BacklogFactsFor(ctx, k.ID)
	if err != nil || len(facts.Sessions) == 0 || len(facts.PRs)+len(facts.AckedPRs) > 0 {
		return k
	}
	for _, sess := range facts.Sessions {
		if !sess.Terminated {
			return k
		}
	}
	counts := make(map[domain.BacklogLinkKind]int, len(k.LinkCounts))
	for kind, n := range k.LinkCounts {
		counts[kind] = n
	}
	delete(counts, domain.BacklogLinkSession)
	k.LinkCounts = counts
	return k
}

// planChildStarted: work began on the child (a session or PR is linked).
func planChildStarted(k domain.BacklogItem) bool {
	return k.LinkCounts[domain.BacklogLinkSession] > 0 || k.LinkCounts[domain.BacklogLinkPR] > 0
}

// planChildSurvives: a done child, or an open one work already began on, is
// kept across a re-plan. Everything else is superseded.
func planChildSurvives(k domain.BacklogItem) bool {
	return k.State == domain.BacklogDone || (k.State == domain.BacklogOpen && planChildStarted(k))
}

func countSurvivors(old []domain.BacklogItem) int {
	n := 0
	for _, k := range old {
		if planChildSurvives(k) {
			n++
		}
	}
	return n
}

// retirePlanChildren settles the previous plan's children once a re-plan is
// approved: unstarted open ones are dropped (superseded by re-plan); the rest
// are kept and lose plan.pr, as they belong to the superseded plan.
func (s *Service) retirePlanChildren(ctx context.Context, parent domain.BacklogItem, old []domain.BacklogItem, deleteDropped bool) {
	for _, k := range old {
		if k.State == domain.BacklogOpen && !planChildStarted(k) {
			if _, err := s.store.SetBacklogItemState(ctx, k.ID, domain.BacklogDropped, "superseded by re-plan", s.now().UTC()); err != nil {
				s.log.Warn("drop superseded plan child", "item", k.Ref(), "error", err)
				continue
			}
			if deleteDropped {
				// Nothing replaces them: delete, so the item is a leaf again and can be started.
				if _, err := s.store.DeleteBacklogItem(ctx, k.ID); err != nil {
					s.log.Warn("delete superseded plan child", "item", k.Ref(), "error", err)
				}
			}
			continue
		}
		if _, err := s.store.UpdateBacklogItem(ctx, k.ID, s.now().UTC(), func(i *domain.BacklogItem) error {
			fields, err := domain.ApplyBacklogField(i.Fields, planPRField, "")
			i.Fields = fields
			return err
		}); err != nil {
			s.log.Warn("clear plan.pr", "item", k.Ref(), "error", err)
		}
	}
	if deleteDropped {
		s.reopenAutoClosed(ctx, parent)
	}
}

// planChildrenBrief lists the surviving children for a re-planner, so its new
// split covers only the remaining work.
func (s *Service) planChildrenBrief(ctx context.Context, parent domain.BacklogItem) string {
	old, err := s.planMadeChildren(ctx, parent)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, k := range old {
		if !planChildSurvives(k) {
			continue
		}
		state := "in progress"
		if k.State == domain.BacklogDone {
			state = "done"
		}
		line := fmt.Sprintf("- %s %s (%s)", k.Ref(), k.Title, state)
		if full, ok, err := s.store.GetBacklogItem(ctx, k.ID); err == nil && ok {
			for _, l := range full.Links {
				if l.Kind == domain.BacklogLinkPR {
					line += " PR " + l.Ref
				}
			}
		}
		b.WriteString(line + "\n")
	}
	if b.Len() == 0 {
		return ""
	}
	return "Earlier work from a previous plan already exists. Plan only the remaining work; do not repeat these:\n" + b.String()
}

// reopenAutoClosed reopens a planned item that closing its last child
// auto-closed ("children closed"): dropping and deleting plan children must not
// leave it done.
func (s *Service) reopenAutoClosed(ctx context.Context, parent domain.BacklogItem) {
	p, ok, err := s.store.GetBacklogItem(ctx, parent.ID)
	if err != nil || !ok || !p.State.Closed() || p.ClosedReason != domain.BacklogReasonChildrenClosed {
		return
	}
	if _, err := s.store.SetBacklogItemState(ctx, parent.ID, domain.BacklogOpen, "", s.now().UTC()); err != nil {
		s.log.Warn("reopen planned item", "item", parent.Ref(), "error", err)
	}
}
