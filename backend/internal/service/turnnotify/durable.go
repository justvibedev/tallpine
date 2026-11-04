package turnnotify

import (
	"context"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
)

// FallbackStore reads committed completion intents awaiting report creation.
type FallbackStore interface {
	ListPendingTurnFallbacks(context.Context) ([]domain.TurnFallback, error)
	HasCurrentCompletedTurnFallback(context.Context, domain.SessionID) (bool, error)
}

// Start recovers durable completions and retries failed report writes. The
// returned channel closes before the daemon closes storage. Close never flushes.
func (o *Observer) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if o.Fallbacks == nil {
			return
		}
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-o.wake:
			case <-timer.C:
			}
			if o.isClosed() {
				return
			}
			delay := o.deliverPending(ctx)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(delay)
		}
	}()
	return done
}

func (o *Observer) wakePending() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

func (o *Observer) durableSignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) bool {
	if o.Fallbacks == nil {
		return false
	}
	rec, found, err := o.Sessions.GetSession(ctx, id)
	if err != nil || !found {
		return false
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		// Only a committed row owns the result; interrupted, auth-wait and
		// unrecorded turns keep the legacy idle timer.
		covered, err := o.Fallbacks.HasCurrentCompletedTurnFallback(ctx, id)
		if err != nil {
			// Prefer a possible duplicate over silence: arm the legacy timer.
			o.Log.Warn("check durable turn notification", "session", id, "err", err)
			return false
		}
		return covered
	}
	return rec.Harness == domain.HarnessCodex && s.ProviderTurnID != "" && s.LaunchID != "" && s.AgentSessionID != ""
}

func (o *Observer) deliverPending(ctx context.Context) time.Duration {
	next := 30 * time.Second
	pending, err := o.Fallbacks.ListPendingTurnFallbacks(ctx)
	if err != nil {
		o.Log.Warn("recover turn fallbacks", "err", err)
		return 5 * time.Second
	}
	for _, f := range pending {
		if o.isClosed() || ctx.Err() != nil {
			return next
		}
		if delay := f.DueAt.Sub(o.Now()); delay > 0 {
			if delay < next {
				next = delay
			}
			continue
		}
		rec, found, err := o.Sessions.GetSession(ctx, f.SessionID)
		if err != nil || !found {
			next = 5 * time.Second
			continue
		}
		o.mu.Lock()
		reports := o.Reports
		o.mu.Unlock()
		if reports == nil {
			next = 5 * time.Second
			continue
		}
		message := Message(KindTurnEnded, rec, f.Excerpt)
		if until, auto, limited := domain.ParseUsageLimitFallbackExcerpt(f.Excerpt); limited {
			message = Message(KindUsageLimited, rec, usageLimitDetail(until, auto))
		}
		_, err = reports.Create(ctx, reportsvc.CreateInput{ID: f.ID, TurnFallbackID: f.ID, SessionID: f.SessionID, Message: message, Synthesized: true, Immediate: true, Quiet: true})
		if err != nil && !errors.Is(err, domain.ErrDuplicateReport) && !errors.Is(err, domain.ErrTurnFallbackInactive) {
			o.Log.Warn("persist turn fallback report", "session", f.SessionID, "err", err)
			next = 5 * time.Second
		}
	}
	return next
}

// durablyCovered reports whether a committed row now owns this Chat turn's
// notice. An unreadable store reports false so the legacy notice is sent.
func (o *Observer) durablyCovered(id domain.SessionID) bool {
	if o.Fallbacks == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rec, found, err := o.Sessions.GetSession(ctx, id)
	if err != nil || !found || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return false
	}
	covered, err := o.Fallbacks.HasCurrentCompletedTurnFallback(ctx, id)
	return err == nil && covered
}
