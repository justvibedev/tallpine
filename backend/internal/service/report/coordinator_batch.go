package report

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// PreparedBatch is a token-fenced durable batch awaiting acceptance.
type PreparedBatch struct {
	ProjectID domain.ProjectID
	Token     string
	ID        string
	Reports   []domain.ReportRecord
	// Superseded records only status claims replaced by a later claim from
	// the same worker/task. Original reports remain durable; outputs survive.
	Superseded map[string]domain.ReportRecord
}

func (c *Coordinator) claim(ctx context.Context, projectID domain.ProjectID, lane string, target domain.SessionID, roster projectRoster) (PreparedBatch, error) {
	token := c.newToken()
	reports, err := c.store.ClaimPendingReportBatch(ctx, projectID, lane, token, c.now().UTC())
	if err != nil {
		return PreparedBatch{}, err
	}
	// Only a first attempt sweeps. A retried batch may already be typed into a
	// terminal under this key, and accepting that copy would acknowledge reports
	// added since then without the orchestrator ever seeing them.
	if len(reports) > 0 && firstAttempt(reports) {
		extra, sweepErr := c.sweepSharedTargetLanes(ctx, projectID, lane, target, roster, token, reports[0].DeliveryBatchID)
		if sweepErr != nil {
			_ = c.release(ctx, PreparedBatch{ProjectID: projectID, Token: token, Reports: reports}, sweepErr)
			return PreparedBatch{}, sweepErr
		}
		reports = append(reports, extra...)
		sort.SliceStable(reports, func(i, j int) bool {
			if reports[i].CreatedAt.Equal(reports[j].CreatedAt) {
				return reports[i].ID < reports[j].ID
			}
			return reports[i].CreatedAt.Before(reports[j].CreatedAt)
		})
	}
	return c.prepareClaimedBatch(ctx, projectID, token, reports)
}

// firstAttempt reports whether this claim is the batch's first (claiming counts
// the attempt, so a fresh row reads 1).
func firstAttempt(reports []domain.ReportRecord) bool {
	for _, r := range reports {
		if r.DeliveryAttempts > 1 {
			return false
		}
	}
	return true
}

func (c *Coordinator) prepareClaimedBatch(ctx context.Context, projectID domain.ProjectID, token string, reports []domain.ReportRecord) (PreparedBatch, error) {
	reports = c.dropStale(ctx, token, reports)
	for i := range reports {
		session, found, lookupErr := c.store.GetSession(ctx, reports[i].SessionID)
		if lookupErr == nil && found {
			reports[i].SessionDisplayName = strings.TrimSpace(session.DisplayName)
		}
		// Every delivered done is checked, including one released before its
		// settlement deadline by a claim that swept the project's queue. The
		// verifier decides which facts are settled enough to claim.
		if c.verify != nil && reports[i].State == domain.ReportDone {
			reports[i].UnverifiedReason = c.verify(ctx, reports[i])
		}
	}
	batchID := ""
	if len(reports) > 0 {
		batchID = reports[0].DeliveryBatchID
	}
	batch := PreparedBatch{ProjectID: projectID, Token: token, ID: batchID, Reports: reports}
	var err error
	batch.Superseded, err = c.supersededStatuses(ctx, reports)
	if err != nil {
		_ = c.release(ctx, batch, err)
	}
	return batch, err
}

// PreparePiggyback claims pending reports for the active Chat orchestrator.
// The caller must acknowledge only after its user message is durably accepted.
func (c *Coordinator) PreparePiggyback(ctx context.Context, target domain.SessionID) (PreparedBatch, error) {
	if c == nil || c.store == nil {
		return PreparedBatch{}, nil
	}
	rec, ok, err := c.store.GetSession(ctx, target)
	if err != nil || !ok {
		return PreparedBatch{}, err
	}
	if rec.Kind != domain.KindOrchestrator || rec.IsTerminated || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return PreparedBatch{}, nil
	}
	// An earlier refused standalone batch retains its retry identity. Piggyback
	// consumption must also include later batches, otherwise old retry deadlines
	// can hide the progress/done report that made the old status obsolete.
	token := c.newToken()
	lane := ""
	if ls, ok := c.store.(LaneStore); ok {
		sl, err := ls.SessionLane(ctx, target)
		if err != nil {
			return PreparedBatch{}, err
		}
		lane = sl.LaneItemID
	}
	var reports []domain.ReportRecord
	for range scheduleLimit {
		next, err := c.store.ClaimPendingReportBatch(ctx, rec.ProjectID, lane, token, c.now().UTC())
		if err != nil {
			_ = c.release(ctx, PreparedBatch{ProjectID: rec.ProjectID, Token: token, Reports: reports}, err)
			return PreparedBatch{}, err
		}
		if len(next) == 0 {
			break
		}
		reports = append(reports, next...)
	}
	sort.SliceStable(reports, func(i, j int) bool {
		if reports[i].CreatedAt.Equal(reports[j].CreatedAt) {
			return reports[i].ID < reports[j].ID
		}
		return reports[i].CreatedAt.Before(reports[j].CreatedAt)
	})
	return c.prepareClaimedBatch(ctx, rec.ProjectID, token, reports)
}

// AcceptPiggyback acknowledges reports after the combined user turn is accepted.
func (c *Coordinator) AcceptPiggyback(ctx context.Context, batch PreparedBatch) error {
	if len(batch.Reports) == 0 {
		return nil
	}
	_, err := c.store.AcknowledgeReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC())
	return err
}

// ReleasePiggyback makes a batch retryable when the combined turn was refused.
func (c *Coordinator) ReleasePiggyback(ctx context.Context, batch PreparedBatch, cause error) error {
	if len(batch.Reports) == 0 {
		return nil
	}
	return c.release(ctx, batch, cause)
}

func (c *Coordinator) release(ctx context.Context, batch PreparedBatch, cause error) error {
	_, releaseErr := c.store.ReleaseReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC(), cause.Error())
	if releaseErr != nil {
		return errors.Join(cause, releaseErr)
	}
	return cause
}

// LaneSweeper is an optional DeliveryStore capability that lets a due lane's
// batch carry the never-attempted reports of other lanes.
type LaneSweeper interface {
	SweepFreshLaneReports(ctx context.Context, projectID domain.ProjectID, lane, batchID, token string, at time.Time) ([]domain.ReportRecord, error)
}

// sweepSharedTargetLanes adds to a due lane's batch the fresh pending reports
// (e.g. a held quiet turn_ended) of every other lane that resolves to the same
// orchestrator, whether or not that lane is due. Without it a held report
// arrives out of order, after the immediate ones that share its orchestrator.
func (c *Coordinator) sweepSharedTargetLanes(ctx context.Context, projectID domain.ProjectID, lane string, target domain.SessionID, roster projectRoster, token, batchID string) ([]domain.ReportRecord, error) {
	ls, ok := c.store.(LaneStore)
	sw, ok2 := c.store.(LaneSweeper)
	if !ok || !ok2 || batchID == "" {
		return nil, nil
	}
	lanes, err := ls.ListPendingReportLanes(ctx, projectID, c.now().UTC().AddDate(100, 0, 0))
	if err != nil {
		return nil, err
	}
	var out []domain.ReportRecord
	for _, other := range lanes {
		if other == lane {
			continue
		}
		if t, found := roster.activeOrchestrator(other); !found || t.ID != target {
			continue
		}
		got, err := sw.SweepFreshLaneReports(ctx, projectID, other, batchID, token, c.now().UTC())
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}
