package usage

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

const (
	defaultWorkerCount            = 2
	defaultWorkQueueSize          = 256
	defaultCoordinatorRetry       = 30 * time.Second
	refreshRetryID          int64 = 0
)

type coordinatorStore interface {
	ListWatchableUsageSources(context.Context) ([]domain.UsageSourceRecord, error)
	HasPendingUsageDiscovery(context.Context) (bool, error)
}

type sourceIngestor interface {
	Ingest(context.Context, int64) (IngestResult, error)
}

type transcriptWatcher interface {
	Events() <-chan TranscriptEvent
	Errors() <-chan error
	Start(context.Context) <-chan struct{}
	Rebuild(context.Context, []string) error
}

// CoordinatorConfig configures the event-driven usage pipeline.
type CoordinatorConfig struct {
	Workers       int
	QueueSize     int
	Clock         func() time.Time
	RetryDelay    time.Duration
	Logger        *slog.Logger
	Initialize    func(context.Context) error
	Reconcile     func(context.Context) error
	ReconcilePath func(context.Context, string) error
}

// Coordinator turns filesystem, hook, startup, and retry signals into bounded
// source ingestion work.
type Coordinator struct {
	store         coordinatorStore
	ingestor      sourceIngestor
	watcher       transcriptWatcher
	workers       int
	queueSize     int
	now           func() time.Time
	retryDelay    time.Duration
	logger        *slog.Logger
	initialize    func(context.Context) error
	reconcile     func(context.Context) error
	reconcilePath func(context.Context, string) error
	refresh       chan struct{}
	inventory     chan struct{}
}

// NewCoordinator constructs an event-driven usage coordinator.
func NewCoordinator(
	store coordinatorStore,
	ingestor sourceIngestor,
	watcher transcriptWatcher,
	cfg CoordinatorConfig,
) *Coordinator {
	if cfg.Workers <= 0 {
		cfg.Workers = defaultWorkerCount
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultWorkQueueSize
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = defaultCoordinatorRetry
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Coordinator{
		store:         store,
		ingestor:      ingestor,
		watcher:       watcher,
		workers:       cfg.Workers,
		queueSize:     cfg.QueueSize,
		now:           cfg.Clock,
		retryDelay:    cfg.RetryDelay,
		logger:        cfg.Logger,
		initialize:    cfg.Initialize,
		reconcile:     cfg.Reconcile,
		reconcilePath: cfg.ReconcilePath,
		refresh:       make(chan struct{}, 1),
		inventory:     make(chan struct{}, 1),
	}
}

// NotifySourcesChanged requests source discovery and inventory rebuilding
// without blocking an agent hook.
func (c *Coordinator) NotifySourcesChanged() {
	select {
	case c.refresh <- struct{}{}:
	default:
	}
}

// NotifyInventoryChanged requests a source inventory rebuild without running
// provider-specific discovery.
func (c *Coordinator) NotifyInventoryChanged() {
	select {
	case c.inventory <- struct{}{}:
	default:
	}
}

type sourceWorkState struct {
	queued  bool
	running bool
	dirty   bool
}

type ingestionResult struct {
	sourceID int64
	result   IngestResult
	err      error
}

// Start runs until ctx is canceled and returns a channel closed after the
// watcher and ingestion workers have stopped.
func (c *Coordinator) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.run(ctx)
	}()
	return done
}

func (c *Coordinator) run(ctx context.Context) {
	watcherDone := c.watcher.Start(ctx)
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	work := make(chan int64, c.queueSize)
	results := make(chan ingestionResult, c.workers)
	var workers sync.WaitGroup
	for range c.workers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for sourceID := range work {
				result, err := c.ingestor.Ingest(workerCtx, sourceID)
				select {
				case results <- ingestionResult{sourceID: sourceID, result: result, err: err}:
				case <-workerCtx.Done():
					return
				}
			}
		}()
	}
	defer func() {
		cancelWorkers()
		close(work)
		workers.Wait()
		<-watcherDone
	}()

	l := &coordinatorLoop{
		c: c, ctx: ctx,
		states:            make(map[int64]*sourceWorkState),
		retries:           make(map[int64]time.Time),
		paths:             make(map[string][]int64),
		initializePending: c.initialize != nil,
	}
	// Watch roots are active before reconciliation, closing the startup gap
	// between reading an offset and beginning to receive filesystem events.
	l.refreshInventory(true)

	events := c.watcher.Events()
	watcherErrors := c.watcher.Errors()
	for {
		var workOut chan int64
		var nextSourceID int64
		if len(l.ready) > 0 {
			workOut = work
			nextSourceID = l.ready[0]
		}
		retryC := armRetryTimer(&l.retryTimer, l.retries, c.now().UTC())

		select {
		case <-ctx.Done():
			if l.retryTimer != nil {
				stopTimer(l.retryTimer)
			}
			return
		case workOut <- nextSourceID:
			l.ready = l.ready[1:]
			state := l.states[nextSourceID]
			state.queued = false
			state.running = true
		case completed := <-results:
			l.handleCompleted(completed)
		case event, ok := <-events:
			if !ok {
				c.logger.Warn("usage transcript watcher event channel closed")
				return
			}
			l.handleWatchEvent(event)
		case err, ok := <-watcherErrors:
			if !ok {
				c.logger.Warn("usage transcript watcher error channel closed")
				return
			}
			l.recoverWatcher(err)
		case <-c.refresh:
			l.refreshInventory(true)
		case <-c.inventory:
			l.refreshInventory(false)
		case <-retryC:
			l.fireDueRetries()
		}
	}
}

func canonicalTranscriptPath(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	// Rename/remove events arrive after the leaf stopped existing. Resolve the
	// parent so aliases such as macOS /var and /private/var still identify the
	// same durable source.
	if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		return filepath.Join(parent, filepath.Base(path))
	}
	return path
}

func armRetryTimer(timer **time.Timer, retries map[int64]time.Time, now time.Time) <-chan time.Time {
	if *timer != nil {
		stopTimer(*timer)
	}
	var earliest time.Time
	for _, at := range retries {
		if earliest.IsZero() || at.Before(earliest) {
			earliest = at
		}
	}
	if earliest.IsZero() {
		return nil
	}
	delay := earliest.Sub(now)
	if delay < 0 {
		delay = 0
	}
	if *timer == nil {
		*timer = time.NewTimer(delay)
	} else {
		(*timer).Reset(delay)
	}
	return (*timer).C
}

func stopTimer(timer *time.Timer) {
	if timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}
