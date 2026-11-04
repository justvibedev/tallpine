package usage

import (
	"context"
	"time"
)

// coordinatorLoop is the single-goroutine state of Coordinator.run: which
// sources are queued, running or waiting on a retry, and which transcript
// paths map to which sources.
type coordinatorLoop struct {
	c                 *Coordinator
	ctx               context.Context
	states            map[int64]*sourceWorkState
	retries           map[int64]time.Time
	paths             map[string][]int64
	ready             []int64
	retryTimer        *time.Timer
	initializePending bool
}

func (l *coordinatorLoop) enqueue(sourceID int64) {
	delete(l.retries, sourceID)
	state := l.states[sourceID]
	if state == nil {
		state = &sourceWorkState{}
		l.states[sourceID] = state
	}
	if state.running {
		state.dirty = true
		return
	}
	if state.queued {
		return
	}
	state.queued = true
	l.ready = append(l.ready, sourceID)
}

func (l *coordinatorLoop) scheduleRetry(sourceID int64, at time.Time) {
	if sourceID != refreshRetryID {
		if state := l.states[sourceID]; state != nil && (state.queued || state.running || state.dirty) {
			return
		}
	}
	l.retries[sourceID] = at.UTC()
}

// scheduleRefreshRetry retries the inventory refresh after the retry delay.
func (l *coordinatorLoop) scheduleRefreshRetry() {
	l.scheduleRetry(refreshRetryID, l.c.now().UTC().Add(l.c.retryDelay))
}

func (l *coordinatorLoop) refreshInventory(runDiscovery bool) {
	c, ctx := l.c, l.ctx
	if l.initializePending {
		if err := c.initialize(ctx); err != nil {
			if ctx.Err() == nil {
				c.logger.Warn("usage source initialization failed", "err", err)
				l.scheduleRefreshRetry()
			}
		} else {
			l.initializePending = false
		}
	}
	if runDiscovery && c.reconcile != nil {
		if err := c.reconcile(ctx); err != nil && ctx.Err() == nil {
			c.logger.Warn("usage source reconciliation failed", "err", err)
			l.scheduleRefreshRetry()
		}
	}
	sources, err := c.store.ListWatchableUsageSources(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.logger.Warn("load watchable usage sources failed", "err", err)
			l.scheduleRefreshRetry()
		}
		return
	}
	nextPaths := make(map[string][]int64)
	live := make(map[int64]struct{}, len(sources))
	now := c.now().UTC()
	for _, source := range sources {
		live[source.ID] = struct{}{}
		path := canonicalTranscriptPath(source.ArtifactPath)
		nextPaths[path] = append(nextPaths[path], source.ID)
		if source.NextRetryAt != nil && source.NextRetryAt.After(now) {
			l.scheduleRetry(source.ID, *source.NextRetryAt)
			continue
		}
		l.enqueue(source.ID)
	}
	sourcePaths := make([]string, 0, len(nextPaths))
	for path := range nextPaths {
		sourcePaths = append(sourcePaths, path)
	}
	if err := c.watcher.Rebuild(ctx, sourcePaths); err != nil {
		if ctx.Err() == nil {
			c.logger.Warn("sync usage transcript watcher failed", "err", err)
			l.scheduleRefreshRetry()
		}
		return
	}
	for sourceID := range l.retries {
		if sourceID == refreshRetryID {
			continue
		}
		if _, ok := live[sourceID]; !ok {
			delete(l.retries, sourceID)
		}
	}
	l.paths = nextPaths
	pending, err := c.store.HasPendingUsageDiscovery(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.logger.Warn("check pending usage discovery failed", "err", err)
			l.scheduleRefreshRetry()
		}
	} else if pending {
		if _, scheduled := l.retries[refreshRetryID]; !scheduled {
			l.scheduleRefreshRetry()
		}
	} else {
		delete(l.retries, refreshRetryID)
	}
}

func (l *coordinatorLoop) recoverWatcher(err error) {
	if err != nil {
		l.c.logger.Warn("usage transcript watcher failed; rebuilding", "err", err)
	}
	// Inventory refresh performs one rebuild with the latest durable paths;
	// discovery first recovers files created while fsnotify was unreliable.
	l.refreshInventory(true)
}

// handleCompleted folds one finished ingestion back into the work state.
func (l *coordinatorLoop) handleCompleted(completed ingestionResult) {
	c, ctx := l.c, l.ctx
	state := l.states[completed.sourceID]
	if state == nil {
		state = &sourceWorkState{}
		l.states[completed.sourceID] = state
	}
	state.running = false
	if completed.err != nil && ctx.Err() == nil {
		c.logger.Warn("usage source ingestion failed", "source", completed.sourceID, "err", completed.err)
	}
	if completed.result.RetryAt != nil {
		l.scheduleRetry(completed.sourceID, *completed.result.RetryAt)
	} else if completed.err != nil && ctx.Err() == nil {
		l.scheduleRetry(completed.sourceID, c.now().UTC().Add(c.retryDelay))
	}
	if completed.result.SyncWatch {
		sourcePaths := make([]string, 0, len(l.paths))
		for path := range l.paths {
			sourcePaths = append(sourcePaths, path)
		}
		if err := c.watcher.Rebuild(ctx, sourcePaths); err != nil && ctx.Err() == nil {
			c.logger.Warn("restore usage transcript watch failed", "err", err)
			l.scheduleRefreshRetry()
		}
	}
	if completed.result.ReconcilePath != "" && c.reconcilePath != nil {
		if err := c.reconcilePath(ctx, completed.result.ReconcilePath); err != nil && ctx.Err() == nil {
			c.logger.Warn("usage source path reconciliation failed", "err", err)
		} else {
			l.refreshInventory(false)
		}
	}
	if completed.result.Reconcile {
		l.refreshInventory(true)
	} else if completed.result.Refresh {
		l.refreshInventory(false)
	}
	if completed.result.ReplacementSourceID != 0 {
		l.enqueue(completed.result.ReplacementSourceID)
	}
	if completed.result.More {
		state.dirty = true
	}
	if state.dirty {
		state.dirty = false
		l.enqueue(completed.sourceID)
	}
}

// handleWatchEvent queues the sources behind a changed transcript path,
// reconciling or discovering the path first when it is not yet known.
func (l *coordinatorLoop) handleWatchEvent(event TranscriptEvent) {
	c, ctx := l.c, l.ctx
	path := canonicalTranscriptPath(event.Path)
	sourceIDs := l.paths[path]
	if len(sourceIDs) == 0 {
		if c.reconcilePath != nil {
			if err := c.reconcilePath(ctx, path); err != nil && ctx.Err() == nil {
				c.logger.Warn("usage source path reconciliation failed", "err", err)
			} else {
				l.refreshInventory(false)
				sourceIDs = l.paths[path]
			}
		}
	}
	if len(sourceIDs) == 0 && event.Discovery {
		l.refreshInventory(true)
		sourceIDs = l.paths[path]
	}
	for _, sourceID := range sourceIDs {
		l.enqueue(sourceID)
	}
}

// fireDueRetries re-queues every source whose retry time has passed.
func (l *coordinatorLoop) fireDueRetries() {
	now := l.c.now().UTC()
	for sourceID, at := range l.retries {
		if at.After(now) {
			continue
		}
		delete(l.retries, sourceID)
		if sourceID == refreshRetryID {
			l.recoverWatcher(nil)
			continue
		}
		l.enqueue(sourceID)
	}
}
