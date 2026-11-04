package backlog

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"tallpine.eu/tallpine/backend/internal/cdc"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// Subscribable is the CDC broadcaster seam.
type Subscribable interface {
	Subscribe(func(cdc.Event)) (unsubscribe func())
}

// SubscriberConfig tunes the lifecycle consumer.
type SubscriberConfig struct {
	// Debounce batches events before reconciling (default 2s).
	Debounce time.Duration
	// SweepEvery paces the full sweep (default 1h); the first one runs after StartDelay (default 30s).
	SweepEvery, StartDelay time.Duration
	Logger                 *slog.Logger
}

// SubscriberStats counts the work the consumer does, to judge its cost.
type SubscriberStats struct {
	EventsSeen, KeysReconciled, Sweeps int64
}

// Subscriber feeds lifecycle triggers into Reconcile: it listens to the CDC
// stream for PRs reaching a terminal state and for session updates, and sweeps
// every live linked item at startup and hourly to repair missed events (the
// poller skips events from downtime). The callback it registers never blocks:
// it records the PR url or session id and a worker reconciles the batch.
type Subscriber struct {
	svc *Service
	cfg SubscriberConfig

	mu      sync.Mutex
	pending map[pendingKey]struct{}
	wake    chan struct{}

	seen, reconciled, sweeps atomic.Int64
}

type pendingKey struct {
	kind domain.BacklogLinkKind
	ref  string
}

// NewSubscriber builds the consumer.
func NewSubscriber(svc *Service, cfg SubscriberConfig) *Subscriber {
	if cfg.Debounce <= 0 {
		cfg.Debounce = 2 * time.Second
	}
	if cfg.SweepEvery <= 0 {
		cfg.SweepEvery = time.Hour
	}
	if cfg.StartDelay <= 0 {
		cfg.StartDelay = 30 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Subscriber{svc: svc, cfg: cfg, pending: map[pendingKey]struct{}{}, wake: make(chan struct{}, 1)}
}

// Stats returns the counters.
func (s *Subscriber) Stats() SubscriberStats {
	return SubscriberStats{EventsSeen: s.seen.Load(), KeysReconciled: s.reconciled.Load(), Sweeps: s.sweeps.Load()}
}

// Handle is the broadcaster callback. It only records work.
func (s *Subscriber) Handle(e cdc.Event) {
	switch e.Type {
	case cdc.EventPRUpdated, cdc.EventPRCreated:
		var p struct {
			URL   string `json:"url"`
			State string `json:"state"`
		}
		if json.Unmarshal(e.Payload, &p) != nil || p.URL == "" || (p.State != "merged" && p.State != "closed") {
			return
		}
		s.enqueue(domain.BacklogLinkPR, p.URL)
		// A PR the worker owns is found through its session link too.
		if e.SessionID != "" {
			s.enqueue(domain.BacklogLinkSession, e.SessionID)
		}
	case cdc.EventSessionUpdated:
		if e.SessionID != "" {
			s.enqueue(domain.BacklogLinkSession, e.SessionID)
		}
	}
}

func (s *Subscriber) enqueue(kind domain.BacklogLinkKind, ref string) {
	s.seen.Add(1)
	s.mu.Lock()
	s.pending[pendingKey{kind, ref}] = struct{}{}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Start subscribes and runs until ctx ends; the returned channel closes once the worker has stopped.
func (s *Subscriber) Start(ctx context.Context, src Subscribable) <-chan struct{} {
	unsubscribe := src.Subscribe(s.Handle)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer unsubscribe()
		sweep := time.NewTimer(s.cfg.StartDelay)
		defer sweep.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.wake:
				select {
				case <-ctx.Done():
					return
				case <-time.After(s.cfg.Debounce):
				}
				s.Drain(ctx)
			case <-sweep.C:
				s.sweeps.Add(1)
				if err := s.svc.Sweep(ctx); err != nil && ctx.Err() == nil {
					s.cfg.Logger.Warn("backlog lifecycle sweep failed", "error", err)
				}
				sweep.Reset(s.cfg.SweepEvery)
			}
		}
	}()
	return done
}

// Drain reconciles every recorded key. Exposed so tests need no timers.
func (s *Subscriber) Drain(ctx context.Context) {
	s.mu.Lock()
	keys := s.pending
	s.pending = map[pendingKey]struct{}{}
	s.mu.Unlock()
	for k := range keys {
		s.reconciled.Add(1)
		if err := s.svc.ReconcileLinked(ctx, k.kind, k.ref); err != nil && ctx.Err() == nil {
			s.cfg.Logger.Warn("backlog lifecycle reconcile failed", "kind", k.kind, "ref", k.ref, "error", err)
		}
	}
}
