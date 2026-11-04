package backlog

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func invalidRun(format string, args ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrBacklogRunInvalid, fmt.Sprintf(format, args...))
}

// resolveRunItem finds an item by ref in the project.
func (s *RunService) resolveRunItem(ctx context.Context, projectID domain.ProjectID, ref string) (domain.BacklogItem, error) {
	seq, ok := domain.ParseBacklogRef(ref)
	if !ok {
		return domain.BacklogItem{}, invalidRun("%q is not an item ref", ref)
	}
	item, found, err := s.store.GetBacklogItemByRef(ctx, projectID, seq)
	if err != nil {
		return domain.BacklogItem{}, err
	}
	if !found {
		return domain.BacklogItem{}, apierr.NotFound("BACKLOG_ITEM_NOT_FOUND", "Unknown backlog item "+ref)
	}
	return item, nil
}

type plannedItem struct {
	item      domain.BacklogItem
	lane, pos int
	model     string
}

// validatePlan checks the orchestrator's plan and returns it ready to save.
// Rules: at least one item, no duplicates, every item open (or already in the
// run), no parents (their children run instead), a model on every item, and
// every blocker is either closed or earlier in the same lane. Items in other
// lanes run in parallel, so a cross-lane dependency would not hold.
func (s *RunService) validatePlan(ctx context.Context, run domain.BacklogRun, existing []domain.BacklogRunItem, in RunPlanInput) ([]store.BacklogRunPlanItem, error) {
	inRun := map[string]bool{}
	for _, it := range existing {
		inRun[it.ItemID] = true
	}
	planned := map[string]plannedItem{}
	var order []string
	for lane, items := range in.Lanes {
		for pos, p := range items {
			item, err := s.resolveRunItem(ctx, run.ProjectID, p.Ref)
			if err != nil {
				return nil, err
			}
			model := strings.TrimSpace(p.Model)
			switch {
			case model == "" || utf8.RuneCountInString(model) > domain.MaxBacklogRunModel:
				return nil, invalidRun("%s needs a model of at most %d characters", item.Ref(), domain.MaxBacklogRunModel)
			case planned[item.ID].item.ID != "":
				return nil, invalidRun("%s appears twice in the plan", item.Ref())
			case item.State.Closed() && !inRun[item.ID]:
				return nil, invalidRun("%s is %s", item.Ref(), item.State)
			}
			if !item.State.Closed() && !inRun[item.ID] && item.State != domain.BacklogOpen {
				return nil, invalidRun("%s is %s, accept it first", item.Ref(), item.State)
			}
			children, err := s.store.CountBacklogChildren(ctx, item.ID)
			if err != nil {
				return nil, err
			}
			if children > 0 {
				return nil, invalidRun("%s is a parent; plan its children instead", item.Ref())
			}
			planned[item.ID] = plannedItem{item: item, lane: lane, pos: pos, model: model}
			order = append(order, item.ID)
		}
	}
	if len(order) == 0 {
		return nil, invalidRun("a plan needs at least one item")
	}
	for _, id := range order {
		if err := s.checkBlockers(ctx, planned, planned[id]); err != nil {
			return nil, err
		}
	}
	out := make([]store.BacklogRunPlanItem, 0, len(order))
	for _, id := range order {
		p := planned[id]
		out = append(out, store.BacklogRunPlanItem{ItemID: id, Lane: p.lane, Position: p.pos, Model: p.model})
	}
	return out, nil
}

func (s *RunService) checkBlockers(ctx context.Context, planned map[string]plannedItem, p plannedItem) error {
	for _, link := range p.item.Links {
		if link.Kind != domain.BacklogLinkBlockedBy {
			continue
		}
		if blocker, ok := planned[link.Ref]; ok {
			if blocker.lane != p.lane || blocker.pos >= p.pos {
				return invalidRun("%s is blocked by %s, so it must come after it in the same lane", p.item.Ref(), blocker.item.Ref())
			}
			continue
		}
		blocker, found, err := s.store.GetBacklogItem(ctx, link.Ref)
		if err != nil {
			return err
		}
		if found && !blocker.State.Closed() {
			return invalidRun("%s is blocked by %s, which is not done and not in the plan", p.item.Ref(), blocker.Ref())
		}
	}
	return nil
}
