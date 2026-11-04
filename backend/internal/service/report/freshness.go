package report

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func (b PreparedBatch) hasDeliverable() bool {
	for _, r := range b.Reports {
		if _, superseded := b.Superseded[r.ID]; !superseded || len(r.Outputs) > 0 && !b.actedOn(r.ID) {
			return true
		}
	}
	return false
}

// RefreshDelivery upgrades already accepted queues from older daemons at the
// last boundary before consumption. It never treats a user message as a report:
// the batch identity must exist in this orchestrator's durable report outbox.
func (c *Coordinator) RefreshDelivery(ctx context.Context, target domain.SessionID, batchID string) (string, bool, error) {
	session, found, err := c.store.GetSession(ctx, target)
	if err != nil || !found || session.Kind != domain.KindOrchestrator {
		return "", false, err
	}
	reader, ok := c.store.(interface {
		ListReportsByProject(context.Context, domain.ProjectID) ([]domain.ReportRecord, error)
	})
	if !ok {
		return "", false, nil
	}
	reports, err := reader.ListReportsByProject(ctx, session.ProjectID)
	if err != nil {
		return "", false, err
	}
	batch := PreparedBatch{ProjectID: session.ProjectID, ID: batchID}
	for _, r := range reports {
		if r.DeliveryBatchID == batchID {
			batch.Reports = append(batch.Reports, r)
		}
	}
	if len(batch.Reports) == 0 {
		return "", false, nil
	}
	batch.Superseded, err = c.supersededStatuses(ctx, batch.Reports)
	if err != nil || !batch.hasDeliverable() {
		return "", true, err
	}
	return batch.Render(), true, nil
}

// Only checkpoint/stuck are replaceable status claims. Information, outputs,
// done results and requests for a decision are not a latest-value register.
// In particular, progress is not proof that a needs_input request was resolved.
func (c *Coordinator) supersededStatuses(ctx context.Context, reports []domain.ReportRecord) (map[string]domain.ReportRecord, error) {
	out := make(map[string]domain.ReportRecord)
	byWorker := make(map[domain.SessionID][]domain.ReportRecord)
	for _, r := range reports {
		byWorker[r.SessionID] = append(byWorker[r.SessionID], r)
	}
	reader, canRead := c.store.(interface {
		ListReportsBySession(context.Context, domain.SessionID) ([]domain.ReportRecord, error)
	})
	for worker, pending := range byWorker {
		known := pending
		if canRead {
			var err error
			known, err = reader.ListReportsBySession(ctx, worker)
			if err != nil {
				return nil, err
			}
		}
		session, found, err := c.store.GetSession(ctx, worker)
		if err != nil {
			return nil, err
		}
		actions := c.workerActions(ctx, worker)
		taskBoundary := session.Metadata.LatestUserPromptAt
		if session.Metadata.LatestInteractionAt.After(taskBoundary) {
			taskBoundary = session.Metadata.LatestInteractionAt
		}
		for _, r := range pending {
			if supersededByAction(r, actions) {
				out[r.ID] = domain.ReportRecord{ID: r.ID, Note: actedOnMarker}
				continue
			}
			if r.State != domain.ReportCheckpoint && r.State != domain.ReportStuck {
				continue
			}
			// A newer real task direction is a boundary, not evidence that an
			// older task's checkpoint/request has been answered by this task.
			if found && r.CreatedAt.Before(taskBoundary) {
				continue
			}
			for _, next := range known {
				if next.State == "" || !next.CreatedAt.After(r.CreatedAt) || reportKey(next) == reportKey(r) {
					continue
				}
				if previous, ok := out[r.ID]; !ok || next.CreatedAt.After(previous.CreatedAt) {
					out[r.ID] = next
				}
			}
		}
	}
	return out, nil
}
