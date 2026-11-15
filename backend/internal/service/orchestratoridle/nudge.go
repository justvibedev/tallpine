// Package orchestratoridle nudges an orchestrator that went quiet with owner
// intents still open and nothing in flight (B713). The project opts in with
// orchestratorIdleNudge; the nudge goes out once per idle period.
package orchestratoridle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// Store is what the sweep reads.
type Store interface {
	ListLiveSessions(ctx context.Context) ([]domain.SessionRecord, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
	ListRunningReviewRunsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error)
	SelectOrchestratorIntents(ctx context.Context, project domain.ProjectID, includeDone bool) ([]domain.OrchestratorIntent, error)
	ListOpenOwnerAsks(ctx context.Context, project domain.ProjectID) ([]domain.OwnerAsk, error)
}

// Deps wires the sweep.
type Deps struct {
	Store Store
	// Send delivers the nudge through the guarded lifecycle nudge; false means
	// the guard refused (the session waits on its user, say) and nothing was sent.
	Send func(ctx context.Context, to domain.SessionID, msg string) (bool, error)
	Now  func() time.Time
	Log  *slog.Logger
}

// Sweeper decides and sends idle nudges.
type Sweeper struct {
	deps Deps
	mu   sync.Mutex
	// nudged holds, per orchestrator, the open-intent count it was nudged at.
	// The idle period ends, and the nudge re-arms, when work is in flight again
	// or the intents change; the nudge's own turn does not end it.
	// In memory: a daemon restart re-arms it and may send one extra nudge.
	nudged map[domain.SessionID]int
}

// New returns a Sweeper.
func New(deps Deps) *Sweeper {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Sweeper{deps: deps, nudged: map[domain.SessionID]int{}}
}

// Sweep nudges every live orchestrator that qualifies.
func (s *Sweeper) Sweep(ctx context.Context) error {
	recs, err := s.deps.Store.ListLiveSessions(ctx)
	if err != nil {
		return fmt.Errorf("list live sessions: %w", err)
	}
	live := map[domain.SessionID]bool{}
	var errs []error
	for _, rec := range recs {
		if rec.Kind != domain.KindOrchestrator || rec.IsTerminated {
			continue
		}
		live[rec.ID] = true
		if err := s.maybe(ctx, rec); err != nil {
			errs = append(errs, fmt.Errorf("idle nudge %s: %w", rec.ID, err))
		}
	}
	s.mu.Lock()
	for id := range s.nudged {
		if !live[id] {
			delete(s.nudged, id)
		}
	}
	s.mu.Unlock()
	return errors.Join(errs...)
}

func (s *Sweeper) maybe(ctx context.Context, rec domain.SessionRecord) error {
	proj, ok, err := s.deps.Store.GetProject(ctx, string(rec.ProjectID))
	if err != nil || !ok || !proj.Config.OrchestratorIdleNudge {
		s.rearm(rec.ID)
		return err
	}
	intents, err := s.deps.Store.SelectOrchestratorIntents(ctx, rec.ProjectID, false)
	if err != nil {
		return err
	}
	if len(intents) == 0 {
		s.rearm(rec.ID)
		return nil
	}
	inFlight, err := s.inFlight(ctx, rec)
	if err != nil {
		return err
	}
	if inFlight {
		s.rearm(rec.ID)
		return nil
	}
	// Mid-turn, waiting on the user or blocked: never inject automated input.
	if rec.Activity.State != domain.ActivityIdle ||
		s.deps.Now().Sub(rec.Activity.LastActivityAt) < domain.OrchestratorIdleNudgeAfter {
		return nil
	}
	s.mu.Lock()
	prev, done := s.nudged[rec.ID]
	if done && prev == len(intents) {
		s.mu.Unlock()
		return nil
	}
	// Recorded before sending: a failed send is not retried every sweep.
	s.nudged[rec.ID] = len(intents)
	s.mu.Unlock()
	sent := false
	defer func() {
		if !sent {
			s.rearm(rec.ID)
		}
	}()
	msg := fmt.Sprintf("[auto] idle with %d open intents and nothing in flight: continue or ask. (`%s orchestrator intent ls`)", len(intents), brand.CLIName)
	ok, err = s.deps.Send(ctx, rec.ID, msg)
	if err != nil {
		return err
	}
	if !ok {
		return nil // refused by the guard: try again once it is idle
	}
	sent = true
	s.deps.Log.Info("idle orchestrator nudged", "session", rec.ID, "openIntents", len(intents))
	return nil
}

func (s *Sweeper) rearm(id domain.SessionID) {
	s.mu.Lock()
	delete(s.nudged, id)
	s.mu.Unlock()
}

// inFlight reports a running worker or review, or an open ask, in rec's project.
func (s *Sweeper) inFlight(ctx context.Context, rec domain.SessionRecord) (bool, error) {
	asks, err := s.deps.Store.ListOpenOwnerAsks(ctx, rec.ProjectID)
	if err != nil || len(asks) > 0 {
		return len(asks) > 0, err
	}
	recs, err := s.deps.Store.ListSessions(ctx, rec.ProjectID)
	if err != nil {
		return false, err
	}
	for _, w := range recs {
		if !w.Activity.State.IsBusy() || w.Kind != domain.KindWorker || w.IsTerminated {
			continue
		}
		return true, nil
	}
	for _, w := range recs {
		if w.Kind != domain.KindWorker {
			continue
		}
		runs, err := s.deps.Store.ListRunningReviewRunsBySession(ctx, w.ID)
		if err != nil || len(runs) > 0 {
			return len(runs) > 0, err
		}
	}
	return false, nil
}
