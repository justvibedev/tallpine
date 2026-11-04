package backlog

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// blockerStates maps each blocked_by target of item to whether it is closed.
// It is read before the transaction; targets that are planned in the run are
// judged again from the run's own rows inside it.
func (s *RunService) blockerStates(ctx context.Context, item domain.BacklogItem) (map[string]bool, error) {
	out := map[string]bool{}
	for _, l := range item.Links {
		if l.Kind != domain.BacklogLinkBlockedBy {
			continue
		}
		b, found, err := s.store.GetBacklogItem(ctx, l.Ref)
		if err != nil {
			return nil, err
		}
		out[l.Ref] = !found || b.State.Closed()
	}
	return out, nil
}

func settledForOrder(it domain.BacklogRunItem) bool {
	return it.Status == domain.BacklogRunItemLanded || it.ItemState.Closed()
}

// checkStartable refuses to start items[idx] while something it waits for is
// unfinished: an earlier item in its lane, or a blocker (planned blockers by
// their run status, unplanned ones by the state read before the transaction).
func checkStartable(items []domain.BacklogRunItem, idx int, blockers map[string]bool) error {
	me := items[idx]
	planned := map[string]bool{}
	for _, it := range items {
		planned[it.ItemID] = true
		if it.ItemID == me.ItemID {
			continue
		}
		if it.Lane == me.Lane && it.Position < me.Position && !settledForOrder(it) {
			return transitionError("%s waits for %s, which is %s", me.ItemRef(), it.ItemRef(), it.Status)
		}
		if closed, isBlocker := blockers[it.ItemID]; isBlocker && !settledForOrder(it) && !closed {
			return transitionError("%s is blocked by %s, which is %s", me.ItemRef(), it.ItemRef(), it.Status)
		}
	}
	for id, closed := range blockers {
		if !planned[id] && !closed {
			return transitionError("%s is blocked by an item that is not done", me.ItemRef())
		}
	}
	return nil
}
