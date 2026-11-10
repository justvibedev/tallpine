package store

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// authoredOrderCandidates applies authored order to a lane's pending reports
// (oldest first). The oldest report's batch goes first. When that is a retry
// batch waiting on a busy orchestrator with no row due yet, newer reports wait
// for it if it is back within ReportHeadOfLineHold and nothing needs the owner (an empty result: the held
// rows were moved to its retry time). Otherwise the batch is skipped and the
// first due row leads, so a long backoff never starves later reports. A batch
// with any due row is claimed as is, so every claim makes progress.
func authoredOrderCandidates(ctx context.Context, q *gen.Queries, projectID domain.ProjectID, lane string, scheduled []gen.Report, at time.Time) ([]gen.Report, error) {
	head := scheduled[0]
	if domain.IsReportPiggyback(ctx) || head.DeliveryBatchID == "" || head.DeliveryAttempts == 0 {
		return scheduled, nil
	}
	next := head.AvailableAt
	for _, r := range scheduled {
		if r.DeliveryBatchID != head.DeliveryBatchID {
			continue
		}
		if !r.AvailableAt.After(at) {
			return scheduled, nil
		}
		if r.AvailableAt.Before(next) {
			next = r.AvailableAt
		}
	}
	// Only an availability retry holds newer reports: while the orchestrator is
	// busy they could not be delivered anyway. Failure backoff (including an
	// unconfirmed batch that needs manual recovery) never does.
	if head.LastError == domain.ReportDeliveryBusyReason && next.Sub(at) <= domain.ReportHeadOfLineHold {
		urgent, err := q.UrgentReportsDue(ctx, gen.UrgentReportsDueParams{ProjectID: string(projectID), LaneItemID: lane, AvailableAt: at})
		if err != nil {
			return nil, err
		}
		if urgent == 0 {
			_, err := q.HoldReportsBehindRetry(ctx, gen.HoldReportsBehindRetryParams{
				BusyReason: domain.ReportDeliveryBusyReason, HoldUntil: next, ProjectID: string(projectID), LaneItemID: lane, BatchID: head.DeliveryBatchID,
			})
			return nil, err
		}
	}
	for i, r := range scheduled {
		if !r.AvailableAt.After(at) {
			return scheduled[i:], nil
		}
	}
	return nil, nil
}
