// Package waitescalation tells the human about a worker's needs-input report
// once it can no longer wait on the orchestrator: the wait timed out, or the
// worker has no live orchestrator. Every pass re-derives the audience from
// stored facts, so an escalation due while the daemon was down is sent on boot.
package waitescalation

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// DefaultInterval is how often the reconciler re-checks waiting workers.
const DefaultInterval = 30 * time.Second

// Store is the read side the reconciler needs.
type Store interface {
	ListLiveSessions(ctx context.Context) ([]domain.SessionRecord, error)
	LatestNeedsInputReports(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.ReportRecord, error)
}

// Notifier stores and resolves notifications.
type Notifier interface {
	Notify(context.Context, ports.NotificationIntent) error
	Resolve(context.Context, ports.NotificationResolution) error
}

// Reconciler escalates due orchestrator waits.
type Reconciler struct {
	store    Store
	notifier Notifier
	clock    func() time.Time
	logger   *slog.Logger

	mu        sync.Mutex
	escalated map[domain.SessionID]bool // sessions this process escalated, pruned once resolved
	swept     bool                      // whether the boot pass has cleaned up stale escalations
}

// New builds a Reconciler. clock and logger default to the wall clock and slog.
func New(store Store, notifier Notifier, clock func() time.Time, logger *slog.Logger) *Reconciler {
	if clock == nil {
		clock = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{store: store, notifier: notifier, clock: clock, logger: logger, escalated: map[domain.SessionID]bool{}}
}

// Start runs one pass now (the boot reconcile) and then one per interval until
// ctx ends. The returned channel closes when the loop has stopped.
func (r *Reconciler) Start(ctx context.Context, interval time.Duration) <-chan struct{} {
	if interval <= 0 {
		interval = DefaultInterval
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if err := r.Reconcile(ctx); err != nil && ctx.Err() == nil {
				r.logger.WarnContext(ctx, "needs-input escalation pass failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

// Reconcile sends the escalation notification for each worker whose
// needs-input report now waits on the human, and resolves one whose wait ended.
// Waits that came with an agent-level activity state (waiting_input, blocked)
// were already announced by lifecycle and are skipped here.
func (r *Reconciler) Reconcile(ctx context.Context) error {
	recs, err := r.store.ListLiveSessions(ctx)
	if err != nil {
		return fmt.Errorf("list live sessions: %w", err)
	}
	orchestrators := map[domain.ProjectID]bool{}
	var workers []domain.SessionRecord
	var ids []domain.SessionID
	for _, rec := range recs {
		switch {
		case rec.Kind == domain.KindOrchestrator && rec.Activity.State != domain.ActivityExited:
			orchestrators[rec.ProjectID] = true
		case rec.Kind == domain.KindWorker:
			workers = append(workers, rec)
			ids = append(ids, rec.ID)
		}
	}
	reports, err := r.store.LatestNeedsInputReports(ctx, ids)
	if err != nil {
		return fmt.Errorf("latest needs-input reports: %w", err)
	}
	now := r.clock()
	r.mu.Lock()
	sweep := !r.swept
	r.swept = true
	r.mu.Unlock()
	var firstErr error
	liveWorkers := make(map[domain.SessionID]bool, len(workers))
	for _, rec := range workers {
		liveWorkers[rec.ID] = true
	}
	// A worker that is gone (killed) has nothing left to wait on.
	r.mu.Lock()
	var gone []domain.SessionID
	for id := range r.escalated {
		if !liveWorkers[id] {
			gone = append(gone, id)
		}
	}
	r.mu.Unlock()
	for _, id := range gone {
		if err := r.notifier.Resolve(ctx, ports.NotificationResolution{Type: domain.NotificationNeedsInput, SessionID: id, ResolvedAt: now}); err != nil {
			firstErr = err
			continue
		}
		r.mark(id, false)
	}
	for _, rec := range workers {
		report, has := reports[rec.ID]
		audience := domain.DeriveNeedsInputAudience(domain.NewNeedsInputFacts(rec, report, orchestrators[rec.ProjectID], now))
		switch {
		case has && audience == domain.NeedsInputHuman && !rec.Activity.State.NeedsInput():
			err = r.notifier.Notify(ctx, ports.NotificationIntent{
				Type: domain.NotificationNeedsInput, SessionID: rec.ID, ProjectID: rec.ProjectID,
				CreatedAt: now, SourceKey: domain.NeedsInputEscalationSourceKeyPrefix + report.ID,
				SessionDisplayName: rec.DisplayName,
			})
			if err == nil {
				r.mark(rec.ID, true)
			}
		case audience != domain.NeedsInputHuman && (sweep || r.wasEscalated(rec.ID)):
			err = r.notifier.Resolve(ctx, ports.NotificationResolution{Type: domain.NotificationNeedsInput, SessionID: rec.ID, ResolvedAt: now})
			if err == nil {
				r.mark(rec.ID, false)
			}
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
		err = nil
	}
	return firstErr
}

func (r *Reconciler) mark(id domain.SessionID, escalated bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if escalated {
		r.escalated[id] = true
		return
	}
	delete(r.escalated, id)
}

func (r *Reconciler) wasEscalated(id domain.SessionID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.escalated[id]
}
