package report

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ReportInterruptWindow is the durable per-worker urgent interruption limit.
const ReportInterruptWindow = 3 * time.Minute

// OrchestratorBusyRetry is how long a batch refused with ErrOrchestratorBusy
// waits before its next delivery attempt.
const OrchestratorBusyRetry = 30 * time.Second

// Delivery retry policy. Transient failures back off exponentially per batch
// (the deferred available_at keeps the batch from being re-picked hot) up to
// MaxDeliveryBackoff; after MaxDeliveryAttempts a batch only retries every
// ExhaustedDeliveryRetry.
const (
	MaxDeliveryBackoff = 2 * time.Minute
	// MaxDeliveryAttempts keeps the ~30 min of retries the 10 min cap gave.
	MaxDeliveryAttempts    = 20
	ExhaustedDeliveryRetry = time.Hour
	// UndeliverableRecheck is how often a project parked for lacking an
	// orchestrator is re-examined (Wake also re-examines it immediately).
	UndeliverableRecheck = time.Minute
	// errorLogInterval rate-limits repeated identical delivery error logs.
	errorLogInterval = 5 * time.Minute
	scheduleLimit    = 1000
)

// unconfirmedID names one poisoned batch; lanes of a project may each hold one.
type unconfirmedID struct {
	project domain.ProjectID
	key     string
}

type unconfirmedBatch struct {
	key      string
	since    time.Time
	notified bool
}

// errNoOrchestrator marks a project whose reports cannot be delivered because
// it has no live orchestrator. It is not retried hot.
var errNoOrchestrator = errors.New("project has no active orchestrator")

// ErrReportDeliveryModeUnsupported reports a target without semantic delivery.
var ErrReportDeliveryModeUnsupported = errors.New("report delivery requires a semantic conversation")

// ErrOrchestratorBusy marks a Submit refused because the orchestrator cannot
// take the batch right now (active turn, compaction, queued work, or TUI draft).
// Its durable retry can be released by a later Chat availability signal.
var ErrOrchestratorBusy = errors.New(domain.ReportDeliveryBusyReason)

// ErrDeliveryUnconfirmed marks a batch whose earlier dispatch may have reached
// the provider. It is never resent automatically, so the project is surfaced
// through UndeliverableProjects until someone recovers it.
var ErrDeliveryUnconfirmed = errors.New("worker report delivery unconfirmed")

// DeliveryStore is the durable report scheduling and routing boundary.
type DeliveryStore interface {
	ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error)
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ListPendingReportSchedule(context.Context, int64) ([]domain.ReportRecord, error)
	// ClaimPendingReportBatch claims one lane's pending reports ("" = un-laned).
	ClaimPendingReportBatch(context.Context, domain.ProjectID, string, string, time.Time) ([]domain.ReportRecord, error)
	AcknowledgeReportBatch(context.Context, domain.ProjectID, string, time.Time) (int, error)
	ReleaseReportBatch(context.Context, domain.ProjectID, string, time.Time, string) (int, error)
	DeferReportBatch(context.Context, domain.ProjectID, string, time.Time, string) (int, error)
	ExpediteBusyReports(context.Context, domain.ProjectID, time.Time) error
	AcquireReportInterrupt(context.Context, domain.SessionID, time.Time, time.Duration) (bool, time.Time, error)
}

// ArchivedReportDropper is an optional DeliveryStore capability. A store
// without it leaves an archived project's reports parked.
type ArchivedReportDropper interface {
	GetProject(context.Context, string) (domain.ProjectRecord, bool, error)
	DropPendingProjectReports(context.Context, domain.ProjectID, time.Time, string) ([]domain.ReportRecord, error)
}

// LaneStore is an optional DeliveryStore capability. A store without it routes
// every report to the project's single orchestrator, as before lanes existed.
type LaneStore interface {
	ports.LaneReader
	// ListPendingReportLanes returns the lanes with a report due at the given time.
	ListPendingReportLanes(context.Context, domain.ProjectID, time.Time) ([]string, error)
}

// maxSeenSessions bounds the per-session generation memory in Coordinator.seen.
const maxSeenSessions = 1024

// BatchOpenChecker is an optional DeliveryStore capability used to notice that
// an unconfirmed batch was settled elsewhere (piggyback recovery, archive drop).
type BatchOpenChecker interface {
	ReportBatchOpen(ctx context.Context, project domain.ProjectID, batchID string) (bool, error)
}

// Delivery submits accepted semantic context and interrupts active work.
type Delivery interface {
	Submit(context.Context, domain.SessionID, string, string) error
	Interrupt(context.Context, domain.SessionID) error
}

// Coordinator delivers durable report batches to active orchestrators.
type Coordinator struct {
	store      DeliveryStore
	delivery   Delivery
	now        func() time.Time
	newToken   func() string
	retryDelay time.Duration
	verify     func(context.Context, domain.ReportRecord) string
	notifier   Notifier
	wake       chan struct{}
	rewake     atomic.Bool
	mu         sync.Mutex
	log        *slog.Logger
	// holds delays projects in memory: parked ones lack an orchestrator,
	// others failed before a batch could be claimed (so no durable backoff).
	holds   map[domain.ProjectID]hold
	logged  map[string]time.Time
	statusM sync.Mutex
	parked  map[domain.ProjectID]time.Time
	// unconfirmed holds at most one poisoned batch per lane, for display only.
	unconfirmed map[unconfirmedID]unconfirmedBatch
	// failures counts consecutive delivery failures per batch.
	failures  map[string]int
	available map[domain.SessionID]string
	// seen is the last controller generation that signalled Available.
	seen map[domain.SessionID]string
}

type hold struct {
	until  time.Time
	parked bool
}

// CoordinatorDeps configures durable scheduling and delivery.
type CoordinatorDeps struct {
	Store      DeliveryStore
	Delivery   Delivery
	Now        func() time.Time
	NewToken   func() string
	RetryDelay time.Duration
	Logger     *slog.Logger
	// Verify optionally soft-checks a claimed done report at delivery time. A
	// non-empty result annotates the delivered report; it never blocks delivery.
	Verify func(context.Context, domain.ReportRecord) string
	// Notifier receives reports of a project with no orchestrator, after a grace period.
	Notifier Notifier
}

// NewCoordinator constructs an idle report delivery coordinator.
func NewCoordinator(d CoordinatorDeps) *Coordinator {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.NewToken == nil {
		d.NewToken = uuid.NewString
	}
	if d.RetryDelay <= 0 {
		d.RetryDelay = 5 * time.Second
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	return &Coordinator{
		store: d.Store, delivery: d.Delivery, now: d.Now, newToken: d.NewToken, retryDelay: d.RetryDelay, verify: d.Verify, notifier: d.Notifier,
		wake: make(chan struct{}, 1), log: d.Logger,
		holds: map[domain.ProjectID]hold{}, logged: map[string]time.Time{}, parked: map[domain.ProjectID]time.Time{}, unconfirmed: map[unconfirmedID]unconfirmedBatch{}, failures: map[string]int{}, available: map[domain.SessionID]string{}, seen: map[domain.SessionID]string{},
	}
}

// UndeliverableProjects lists projects whose pending reports are parked because
// no orchestrator is running, with the time each was first parked.
func (c *Coordinator) UndeliverableProjects() map[domain.ProjectID]time.Time {
	out := map[domain.ProjectID]time.Time{}
	if c == nil {
		return out
	}
	c.statusM.Lock()
	defer c.statusM.Unlock()
	for id, since := range c.parked {
		out[id] = since
	}
	for id, u := range c.unconfirmed {
		if _, ok := out[id.project]; !ok {
			out[id.project] = u.since
		}
	}
	return out
}

// Wake asks the coordinator to inspect durable scheduling state.
func (c *Coordinator) Wake() {
	if c == nil {
		return
	}
	c.rewake.Store(true)
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Available signals a committed idle boundary. It never takes the delivery lock
// or calls storage/provider code: callers may still hold Chat lifecycle locks.
// The generation prevents a retired controller from releasing a successor's wait.
func (c *Coordinator) Available(session domain.SessionID, generation string) {
	if c == nil {
		return
	}
	c.statusM.Lock()
	c.available[session] = generation
	fresh := c.seen[session] != generation
	if _, known := c.seen[session]; !known && len(c.seen) >= maxSeenSessions {
		// Bound the map: forgetting costs at most one extra rewake per session.
		c.seen = map[domain.SessionID]string{}
	}
	c.seen[session] = generation
	c.statusM.Unlock()
	// A new orchestrator ends any park hold: drain on this wake, not the recheck.
	// Later idle boundaries of the same controller do not re-open every hold.
	if fresh {
		c.rewake.Store(true)
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Coordinator) expediteAvailable(ctx context.Context) error {
	c.statusM.Lock()
	available := c.available
	c.available = map[domain.SessionID]string{}
	c.statusM.Unlock()
	var errs []error
	for id, generation := range available {
		rec, found, err := c.store.GetSession(ctx, id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !found || rec.Kind != domain.KindOrchestrator || rec.IsTerminated || rec.Metadata.ControllerGeneration != generation {
			continue
		}
		if err := c.store.ExpediteBusyReports(ctx, rec.ProjectID, c.now().UTC()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Start runs delivery until ctx is cancelled and returns its completion signal.
func (c *Coordinator) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() { defer close(done); c.run(ctx) }()
	return done
}

func (c *Coordinator) run(ctx context.Context) {
	for {
		delay, err := c.RunDue(ctx)
		if err != nil {
			c.logRateLimited("run-due", "report delivery failed", err)
		}
		if delay < 0 && err != nil {
			delay = c.retryDelay
		}
		if delay < 0 {
			delay = time.Hour
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-c.wake:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
}

// RunDue performs bounded due delivery and returns the delay until the next
// durable deadline. Tests call it directly with fake time.
func (c *Coordinator) RunDue(ctx context.Context) (time.Duration, error) {
	if c == nil || c.store == nil || c.delivery == nil {
		return -1, errors.New("report coordinator dependencies are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A Wake means something changed (new report, new orchestrator): give
	// every parked or held project another look.
	if c.rewake.Swap(false) {
		c.holds = map[domain.ProjectID]hold{}
	}
	c.pruneUnconfirmed(ctx)
	var errs []error
	if err := c.expediteAvailable(ctx); err != nil {
		errs = append(errs, err)
	}
	// Projects already seen unarchived in this pass; the check is not repeated.
	checkedActive := map[domain.ProjectID]bool{}
	for {
		pending, err := c.store.ListPendingReportSchedule(ctx, scheduleLimit)
		if err != nil {
			return c.retryDelay, errors.Join(append(errs, err)...)
		}
		now := c.now().UTC()
		next := time.Duration(-1)
		note := func(d time.Duration) {
			if d < 0 {
				d = 0
			}
			if next < 0 || d < next {
				next = d
			}
		}
		live := map[domain.ProjectID]bool{}
		delivered := false
		// pending holds one row per project, earliest first.
		for _, rec := range pending {
			project := rec.ProjectID
			live[project] = true
			if h, ok := c.holds[project]; ok && h.until.After(now) {
				note(h.until.Sub(now))
				continue
			}
			dropped, err := c.dropIfArchived(ctx, project, now, checkedActive)
			if err != nil {
				errs = append(errs, fmt.Errorf("project %s: %w", project, err))
				c.holds[project] = hold{until: now.Add(c.retryDelay)}
				note(c.retryDelay)
				continue
			}
			if dropped {
				// Settled rather than delivered; list again like a delivery.
				delete(c.holds, project)
				c.forgetParked(project)
				delivered = true
				break
			}
			if rec.AvailableAt.After(now) {
				note(rec.AvailableAt.Sub(now))
				continue
			}
			err = c.deliverProject(ctx, project)
			switch {
			case err == nil:
				delete(c.holds, project)
				c.unpark(project)
				delivered = true
			case errors.Is(err, errNoOrchestrator):
				c.park(project, now)
				c.holds[project] = hold{until: now.Add(UndeliverableRecheck), parked: true}
				note(UndeliverableRecheck)
			default:
				errs = append(errs, fmt.Errorf("project %s: %w", project, err))
				c.holds[project] = hold{until: now.Add(c.retryDelay)}
				note(c.retryDelay)
			}
			if delivered {
				break
			}
		}
		if delivered {
			continue
		}
		for project := range c.holds {
			if !live[project] {
				delete(c.holds, project)
				c.unpark(project)
			}
		}
		return next, errors.Join(errs...)
	}
}
