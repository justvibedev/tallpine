package report

import (
	"context"
	"errors"
	"sort"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// deliverProject delivers each lane's pending reports to that lane's
// orchestrator. Lanes are independent: one lane's failure does not hold back
// another, and errNoOrchestrator is reported only when no lane could deliver
// because no orchestrator runs.
func (c *Coordinator) deliverProject(ctx context.Context, projectID domain.ProjectID) error {
	lanes, err := c.pendingLanes(ctx, projectID)
	if err != nil {
		return err
	}
	roster, err := c.loadRoster(ctx, projectID)
	if err != nil {
		return err
	}
	var errs []error
	noOrchestrator := false
	for _, lane := range lanes {
		err := c.deliverLane(ctx, projectID, lane, roster)
		switch {
		case err == nil:
		case errors.Is(err, errNoOrchestrator):
			noOrchestrator = true
		default:
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if noOrchestrator {
		return errNoOrchestrator
	}
	return nil
}

// pendingLanes lists the lanes with a due pending report (a lane whose batch
// is deferred by backoff, busy retry or stuck coalescing waits for its own
// deadline), named lanes first and
// "" (un-laned) last. A lane whose orchestrator is gone needs no special case:
// domain.PickOrchestrator falls back to the default orchestrator for it.
func (c *Coordinator) pendingLanes(ctx context.Context, projectID domain.ProjectID) ([]string, error) {
	ls, ok := c.store.(LaneStore)
	if !ok {
		return []string{""}, nil
	}
	lanes, err := ls.ListPendingReportLanes(ctx, projectID, c.now().UTC())
	if err != nil {
		return nil, err
	}
	if len(lanes) == 0 {
		return []string{""}, nil
	}
	sort.Slice(lanes, func(i, j int) bool { // "" last
		if (lanes[i] == "") != (lanes[j] == "") {
			return lanes[j] == ""
		}
		return lanes[i] < lanes[j]
	})
	return lanes, nil
}

func (c *Coordinator) deliverLane(ctx context.Context, projectID domain.ProjectID, lane string, roster projectRoster) error {
	target, ok := roster.activeOrchestrator(lane)
	if !ok {
		return c.deliverWithoutOrchestrator(ctx, projectID, lane)
	}
	batch, err := c.claim(ctx, projectID, lane, target.ID, roster)
	if err != nil || len(batch.Reports) == 0 {
		return err
	}
	return c.deliverBatch(ctx, target, batch)
}

// deliverBatch submits a claimed batch to target and settles it.
func (c *Coordinator) deliverBatch(ctx context.Context, target domain.SessionRecord, batch PreparedBatch) error {
	var err error
	if !batch.hasDeliverable() {
		_, err = c.store.AcknowledgeReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC())
		return err
	}
	// Terminal orchestrators queue typed input during a turn, and a Ctrl-C there
	// cancels the running turn or a just-submitted prompt, so stuck reports are
	// delivered to them like any other report.
	if batch.hasState(domain.ReportStuck) && domain.NormalizeSessionMode(target.Mode) == domain.SessionModeChat {
		interrupted := false
		var nextInterrupt time.Time
		for _, worker := range batch.stuckWorkers() {
			acquired, next, acquireErr := c.store.AcquireReportInterrupt(ctx, worker, c.now().UTC(), ReportInterruptWindow)
			if acquireErr != nil {
				return c.backoff(ctx, batch, acquireErr)
			}
			if acquired {
				interrupted = true
				if interruptErr := c.delivery.Interrupt(ctx, target.ID); interruptErr != nil {
					return c.backoff(ctx, batch, interruptErr)
				}
			} else if nextInterrupt.IsZero() || next.Before(nextInterrupt) {
				nextInterrupt = next
			}
		}
		if !interrupted && !batch.hasUnthrottledContent() {
			_, deferErr := c.store.DeferReportBatch(ctx, batch.ProjectID, batch.Token, nextInterrupt, "repeated stuck report coalesced inside interrupt window")
			return deferErr
		}
	}
	if err := c.delivery.Submit(ctx, target.ID, batch.Render(), batch.IdempotencyKey()); err != nil {
		if errors.Is(err, ErrOrchestratorBusy) {
			_, deferErr := c.store.DeferReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC().Add(OrchestratorBusyRetry), domain.ReportDeliveryBusyReason)
			return deferErr
		}
		var paused *UsageLimitPausedError
		if errors.As(err, &paused) {
			// Queue without counting a failure. The deferral is short so the next
			// attempt rechecks the pause (a DB read, no turn) and delivery resumes
			// as soon as the session can run again.
			retryAt := paused.Until.Add(time.Second)
			if limit := c.now().UTC().Add(UsageLimitRecheck); limit.Before(retryAt) {
				retryAt = limit
			}
			_, deferErr := c.store.DeferReportBatch(ctx, batch.ProjectID, batch.Token, retryAt, "usage limit until "+paused.Until.UTC().Format(time.RFC3339))
			return deferErr
		}
		if errors.Is(err, ErrDeliveryUnconfirmed) {
			c.markUnconfirmed(ctx, target.ID, batch)
		}
		return c.backoff(ctx, batch, err)
	}
	c.clearFailures(batch)
	_, err = c.store.AcknowledgeReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC())
	return err
}

// projectRoster is a project's sessions and lane bindings, read once per
// delivery tick and shared by every lane of that tick.
type projectRoster struct {
	sessions []domain.SessionRecord
	lanes    map[domain.SessionID]string
}

func (c *Coordinator) loadRoster(ctx context.Context, projectID domain.ProjectID) (projectRoster, error) {
	recs, err := c.store.ListSessions(ctx, projectID)
	if err != nil {
		return projectRoster{}, err
	}
	roster := projectRoster{sessions: recs}
	if ls, ok := c.store.(LaneStore); ok {
		if roster.lanes, err = ls.ListSessionLanes(ctx, projectID); err != nil {
			return projectRoster{}, err
		}
	}
	return roster, nil
}

func (r projectRoster) activeOrchestrator(lane string) (domain.SessionRecord, bool) {
	return domain.PickOrchestrator(r.sessions, r.lanes, lane)
}
