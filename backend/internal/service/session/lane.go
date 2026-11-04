package session

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// deletedLaneLabel stands in for the ref of a lane whose backlog item is gone:
// routing still treats the orchestrator as laned, so the display must too. It
// carries the item id so two deleted lanes stay distinct (the frontend groups
// orchestrators by this label to detect duplicates).
func deletedLaneLabel(itemID string) string { return "(deleted lane " + itemID + ")" }

// laneLabelStore is the optional store capability the lane labels need.
type laneLabelStore interface {
	ports.LaneReader
	BacklogRefsByID(context.Context, []string) (map[string]string, error)
}

// laneLabelBatch holds the display labels of the orchestrators of one read,
// loaded once so a list's cost does not grow with its orchestrators.
type laneLabelBatch struct {
	labels map[domain.SessionID]string
	// workers holds the lane ref of laned workers; kept apart from labels so
	// consumers that read Session.Lane as "this is a lane orchestrator" do not
	// start treating workers as one.
	workers map[domain.SessionID]string
}

// loadLaneLabels reads the lane of every orchestrator in recs: one lane query
// per project plus one batched backlog-ref lookup. The label is decoration, so
// a read failure yields no labels instead of failing the session read.
func (s *Service) loadLaneLabels(ctx context.Context, recs []domain.SessionRecord) *laneLabelBatch {
	store, ok := s.store.(laneLabelStore)
	if !ok {
		return nil
	}
	orchestrators := map[domain.ProjectID][]domain.SessionID{}
	kinds := map[domain.SessionID]domain.SessionKind{}
	for _, rec := range recs {
		// A terminated worker no longer holds its lane open: its session_lane row
		// is history (kept for routing/reports), so it carries no lane label.
		// Otherwise clients listing ended sessions invent lanes nobody runs.
		if rec.Kind == domain.KindWorker && rec.IsTerminated {
			continue
		}
		if rec.Kind == domain.KindOrchestrator || rec.Kind == domain.KindWorker {
			orchestrators[rec.ProjectID] = append(orchestrators[rec.ProjectID], rec.ID)
			kinds[rec.ID] = rec.Kind
		}
	}
	if len(orchestrators) == 0 {
		return nil
	}
	laneOf := map[domain.SessionID]string{}
	var itemIDs []string
	seen := map[string]bool{}
	for project, ids := range orchestrators {
		lanes, err := store.ListSessionLanes(ctx, project)
		if err != nil {
			continue
		}
		for _, id := range ids {
			if lane := lanes[id]; lane != "" {
				laneOf[id] = lane
				if !seen[lane] {
					seen[lane] = true
					itemIDs = append(itemIDs, lane)
				}
			}
		}
	}
	if len(laneOf) == 0 {
		return nil
	}
	refs, err := store.BacklogRefsByID(ctx, itemIDs)
	if err != nil {
		return nil
	}
	batch := &laneLabelBatch{labels: map[domain.SessionID]string{}, workers: map[domain.SessionID]string{}}
	for id, lane := range laneOf {
		label := deletedLaneLabel(lane)
		if ref, found := refs[lane]; found {
			label = ref
		}
		if kinds[id] == domain.KindWorker {
			batch.workers[id] = label
		} else {
			batch.labels[id] = label
		}
	}
	return batch
}

// label is the backlog ref of an orchestrator's lane, "" for workers, the
// default orchestrator, or when the lanes could not be read.
func (b *laneLabelBatch) label(id domain.SessionID) string {
	if b == nil {
		return ""
	}
	return b.labels[id]
}

// workerLabel is the lane ref of a worker spawned inside a lane, "" otherwise.
func (b *laneLabelBatch) workerLabel(id domain.SessionID) string {
	if b == nil {
		return ""
	}
	return b.workers[id]
}
