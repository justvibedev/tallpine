package report

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/backoff"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// dropIfArchived settles the pending reports of an archived project. Archiving
// (tallpine project rm) stops the project's sessions, so no orchestrator will ever
// read them; left pending they would be re-examined, and warned about, forever.
// Each keeps its reason in last_error, readable as the report's undelivered
// reason. It reports whether it settled anything.
func (c *Coordinator) dropIfArchived(ctx context.Context, project domain.ProjectID, now time.Time, checkedActive map[domain.ProjectID]bool) (bool, error) {
	dropper, ok := c.store.(ArchivedReportDropper)
	if !ok || checkedActive[project] {
		return false, nil
	}
	rec, found, err := dropper.GetProject(ctx, string(project))
	if err != nil {
		return false, err
	}
	if found && rec.ArchivedAt.IsZero() {
		checkedActive[project] = true
		return false, nil
	}
	reason := fmt.Sprintf("not delivered: project %s was archived before an orchestrator received this report", project)
	if !found {
		reason = fmt.Sprintf("not delivered: project %s no longer exists", project)
	}
	dropped, err := dropper.DropPendingProjectReports(ctx, project, now, reason)
	if err != nil {
		return false, err
	}
	if len(dropped) == 0 {
		return false, nil
	}
	ids := make([]string, len(dropped))
	for i, r := range dropped {
		ids[i] = r.ID
	}
	c.log.Warn("worker reports dropped undelivered: project is archived",
		"project", project, "count", len(dropped), "reports", ids)
	return true, nil
}

// forgetParked clears parked status without claiming the reports became
// deliverable.
func (c *Coordinator) forgetParked(project domain.ProjectID) {
	c.statusM.Lock()
	delete(c.parked, project)
	c.statusM.Unlock()
}

func (c *Coordinator) park(project domain.ProjectID, now time.Time) {
	c.statusM.Lock()
	_, already := c.parked[project]
	if !already {
		c.parked[project] = now
	}
	c.statusM.Unlock()
	if !already {
		c.log.Warn("worker reports are undeliverable: project has no active orchestrator; they are parked until one starts", "project", project)
	}
}

func (c *Coordinator) unpark(project domain.ProjectID) {
	c.statusM.Lock()
	_, was := c.parked[project]
	delete(c.parked, project)
	c.statusM.Unlock()
	if was {
		c.log.Info("parked worker reports are deliverable again", "project", project)
	}
}

// logRateLimited logs err at most once per errorLogInterval per key.
func (c *Coordinator) logRateLimited(key, msg string, err error) {
	now := c.now()
	c.statusM.Lock()
	last, ok := c.logged[key]
	if ok && now.Sub(last) < errorLogInterval {
		c.statusM.Unlock()
		return
	}
	c.logged[key] = now
	c.statusM.Unlock()
	c.log.Error(msg, "error", err)
}

// backoff defers a claimed batch with exponential delay based on its attempts
// so a failing batch never stays the earliest-due work.
func (c *Coordinator) backoff(ctx context.Context, batch PreparedBatch, cause error) error {
	// Count failures only: delivery_attempts also counts busy and coalesced
	// deferrals, which are not failures.
	c.statusM.Lock()
	c.failures[batch.IdempotencyKey()]++
	failures := c.failures[batch.IdempotencyKey()]
	c.statusM.Unlock()
	_, deferErr := c.store.DeferReportBatch(ctx, batch.ProjectID, batch.Token, c.now().UTC().Add(c.backoffDelay(failures)), cause.Error())
	if deferErr != nil {
		return errors.Join(cause, deferErr)
	}
	return cause
}

// backoffDelay is retryDelay doubled per consecutive failure, capped at
// MaxDeliveryBackoff, and ExhaustedDeliveryRetry after MaxDeliveryAttempts.
func (c *Coordinator) backoffDelay(failures int) time.Duration {
	if failures >= MaxDeliveryAttempts {
		return ExhaustedDeliveryRetry
	}
	return backoff.Doubling(c.retryDelay, MaxDeliveryBackoff, failures)
}

// markUnconfirmed surfaces the one poisoned batch without holding the project:
// later batches still claim and deliver while this one backs off. The target
// orchestrator gets one plain notice per batch, never one per retry.
func (c *Coordinator) markUnconfirmed(ctx context.Context, target domain.SessionID, batch PreparedBatch) {
	key := batch.IdempotencyKey()
	id := unconfirmedID{batch.ProjectID, key}
	c.statusM.Lock()
	u, known := c.unconfirmed[id]
	if !known {
		u = unconfirmedBatch{key: key, since: c.now()}
	}
	c.unconfirmed[id] = u
	c.statusM.Unlock()
	if !known {
		c.log.Warn("worker report batch delivery is unconfirmed and will not be resent; send a Chat message to include pending reports", "project", batch.ProjectID, "batch", key)
	}
	if u.notified {
		return
	}
	err := c.delivery.Submit(ctx, target, unconfirmedNotice(batch), "report-notice:"+key)
	var paused *UsageLimitPausedError
	if errors.Is(err, ErrOrchestratorBusy) || errors.As(err, &paused) {
		return // try again on the next retry of this batch
	}
	if err != nil {
		c.log.Warn("could not deliver the unconfirmed-batch notice", "project", batch.ProjectID, "batch", key, "error", err)
	}
	c.statusM.Lock()
	if cur, ok := c.unconfirmed[id]; ok {
		cur.notified = true
		c.unconfirmed[id] = cur
	}
	c.statusM.Unlock()
}

func unconfirmedNotice(batch PreparedBatch) string {
	var b strings.Builder
	b.WriteString("Worker report delivery is unconfirmed: an earlier delivery of the batch below may or may not have reached you, and Tallpine will not resend it automatically. Check the workers directly (tallpine session ls / their output) for the latest status.\nWorkers and report ids:\n")
	for _, r := range batch.Reports {
		fmt.Fprintf(&b, "- %s (%s): %s\n", r.SessionID, r.State, r.ID)
	}
	return b.String()
}

// pruneUnconfirmed forgets batches that are no longer pending or claimed:
// acknowledged through a piggyback, dropped with an archived project, or
// otherwise settled.
func (c *Coordinator) pruneUnconfirmed(ctx context.Context) {
	checker, ok := c.store.(BatchOpenChecker)
	if !ok {
		return
	}
	c.statusM.Lock()
	snapshot := make(map[unconfirmedID]unconfirmedBatch, len(c.unconfirmed))
	for id, u := range c.unconfirmed {
		snapshot[id] = u
	}
	c.statusM.Unlock()
	for id := range snapshot {
		open, err := checker.ReportBatchOpen(ctx, id.project, id.key)
		if err != nil {
			continue
		}
		if !open {
			c.statusM.Lock()
			delete(c.unconfirmed, id)
			c.statusM.Unlock()
		}
	}
}

func (c *Coordinator) clearFailures(batch PreparedBatch) {
	c.statusM.Lock()
	delete(c.unconfirmed, unconfirmedID{batch.ProjectID, batch.IdempotencyKey()})
	delete(c.failures, batch.IdempotencyKey())
	c.statusM.Unlock()
}
