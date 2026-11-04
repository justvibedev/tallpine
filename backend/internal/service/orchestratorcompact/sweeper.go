// Package orchestratorcompact compacts idle orchestrators between tasks (B204).
//
// An orchestrator answers every report batch with a full turn, so its context
// keeps growing between the owner's tasks. When it has been quiet for a while
// and nothing it coordinates is in flight, compacting it lets the next task
// start small. The compaction keeps the session, so its chat, workers, PR
// claims and review delivery are untouched; the provider's own window (B193)
// still caps it mid-task.
package orchestratorcompact

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Store is what the sweep reads.
type Store interface {
	// ListChatHibernationCandidates lists live, idle, non-hibernated Chat sessions.
	ListChatHibernationCandidates(ctx context.Context) ([]domain.SessionID, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
	ListReportsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReportRecord, error)
	ListRunningReviewRunsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error)
}

// LaneStore reads lane bindings; a store without lanes routes every worker to
// the project's single orchestrator.
type LaneStore interface {
	ListSessionLanes(ctx context.Context, project domain.ProjectID) (map[domain.SessionID]string, error)
}

// ChatState is the chat service's view of one orchestrator.
type ChatState struct {
	Busy        bool
	ContextUsed int64
	Threshold   int64
	Handoff     bool
	// Research reports research mode (B413) is on.
	Research bool
}

// Deps wires the sweep.
type Deps struct {
	Store Store
	// State reads a live orchestrator controller; ok is false when there is
	// none or it cannot compact.
	State func(ctx context.Context, id domain.SessionID) (ChatState, bool)
	// Compact starts the idle compaction.
	Compact func(ctx context.Context, id domain.SessionID) error
	// Handoff replaces the orchestrator with a fresh one (B374); nil means a
	// project that asks for it compacts instead.
	Handoff func(ctx context.Context, id domain.SessionID) error
	// ClearResearch ends the orchestrator's research mode (B413) and reports
	// whether it was on; nil leaves research mode alone.
	ClearResearch func(ctx context.Context, id domain.SessionID) bool
	// BackgroundTasks counts the provider's running background tasks. Nil
	// counts none.
	BackgroundTasks func(id domain.SessionID) int
	Now             func() time.Time
	Log             *slog.Logger
}

// Sweeper decides and starts idle compactions.
type Sweeper struct {
	deps Deps
	mu   sync.Mutex
	// attempts holds, per orchestrator, the context size after its last idle
	// compaction attempt (successful or not), or attemptUnsettled until the
	// attempt's turn ends. While the context stays at that size no real turn
	// has run, so the attempt is not repeated. Activity time cannot tell: the
	// compaction is a turn and moves LastActivityAt itself.
	attempts map[domain.SessionID]int64
	// stalled remembers the stalled workers already logged, by activity time.
	stalled map[domain.SessionID]time.Time
}

// New returns a Sweeper.
func New(deps Deps) *Sweeper {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Sweeper{deps: deps, attempts: map[domain.SessionID]int64{}, stalled: map[domain.SessionID]time.Time{}}
}

// Sweep compacts every idle orchestrator that qualifies.
func (s *Sweeper) Sweep(ctx context.Context) error {
	ids, err := s.deps.Store.ListChatHibernationCandidates(ctx)
	if err != nil {
		return fmt.Errorf("list idle chats: %w", err)
	}
	var errs []error
	for _, id := range ids {
		if _, err := s.maybe(ctx, id, true); err != nil {
			errs = append(errs, fmt.Errorf("idle compaction %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// MaybeCompact compacts the orchestrator if it qualifies now and reports
// whether it started a compaction. Hibernation calls it first, so an
// orchestrator about to sleep wakes up small.
//
// This is the before-hibernate hook, which runs while the session holds the
// hibernate operation, so it never hands off: a handoff retires the session
// and would wait on that operation forever. For a project that chose handoff
// it keeps a qualifying orchestrator awake instead, and the sweep (which holds
// no operation) hands it off.
func (s *Sweeper) MaybeCompact(ctx context.Context, id domain.SessionID) (bool, error) {
	return s.maybe(ctx, id, false)
}

// maybe is MaybeCompact; handoffNow says whether a handoff may run here.
func (s *Sweeper) maybe(ctx context.Context, id domain.SessionID, handoffNow bool) (bool, error) {
	rec, found, err := s.deps.Store.GetSession(ctx, id)
	if err != nil || !found || rec.Kind != domain.KindOrchestrator || rec.IsTerminated {
		return false, err
	}
	s.expireResearch(ctx, rec)
	facts, ok, err := s.facts(ctx, rec)
	if err != nil || !ok {
		return false, err
	}
	if reason := facts.IdleCompactSkipReason(); reason != "" {
		return false, nil
	}
	if facts.Handoff && !handoffNow {
		return true, nil
	}
	// Research mode ends first: the compaction or handoff replaces the context
	// it was raised for, and the window must be back before the agent resumes.
	if s.deps.ClearResearch != nil && s.deps.ClearResearch(ctx, id) {
		s.deps.Log.Info("orchestrator research mode ended by idle compaction", "session", id)
	}
	// Recorded before the outcome is known: a failed attempt must not be
	// retried every sweep either.
	s.mu.Lock()
	s.attempts[id] = attemptUnsettled
	s.mu.Unlock()
	if facts.Handoff {
		err := s.deps.Handoff(ctx, id)
		if err == nil {
			s.deps.Log.Info("idle orchestrator handed off", "session", id, "contextUsed", facts.ContextUsed, "threshold", facts.Threshold)
			return true, nil
		}
		// A refused handoff (say, no orchestrator agent configured) leaves the
		// old orchestrator running; compact it so its context stays bounded.
		s.deps.Log.Warn("idle handoff failed; compacting instead", "session", id, "err", err)
	}
	if err := s.deps.Compact(ctx, id); err != nil {
		return false, err
	}
	s.deps.Log.Info("idle orchestrator compacted", "session", id, "contextUsed", facts.ContextUsed, "threshold", facts.Threshold)
	return true, nil
}

// expireResearch ends research mode of an orchestrator that has had no turn
// for domain.ResearchQuietExpiry.
func (s *Sweeper) expireResearch(ctx context.Context, rec domain.SessionRecord) {
	if s.deps.ClearResearch == nil || s.deps.Now().Sub(rec.Activity.LastActivityAt) < domain.ResearchQuietExpiry {
		return
	}
	if st, ok := s.deps.State(ctx, rec.ID); !ok || !st.Research || st.Busy {
		return
	}
	if s.deps.ClearResearch(ctx, rec.ID) {
		s.deps.Log.Info("orchestrator research mode expired after a quiet hour", "session", rec.ID)
	}
}

// facts gathers what decides rec's idle compaction. The chat state is read
// first: an orchestrator that is busy or below its threshold needs no store
// reads.
func (s *Sweeper) facts(ctx context.Context, rec domain.SessionRecord) (domain.IdleCompactFacts, bool, error) {
	state, ok := s.deps.State(ctx, rec.ID)
	if !ok {
		return domain.IdleCompactFacts{}, false, nil
	}
	now := s.deps.Now()
	facts := domain.IdleCompactFacts{
		Busy:                    state.Busy,
		IdleFor:                 now.Sub(rec.Activity.LastActivityAt),
		ContextUsed:             state.ContextUsed,
		Threshold:               state.Threshold,
		Handoff:                 state.Handoff && s.deps.Handoff != nil,
		CompactedAtThisActivity: s.attempted(rec.ID, state),
	}
	if s.deps.BackgroundTasks != nil {
		facts.BackgroundTasks = s.deps.BackgroundTasks(rec.ID)
	}
	if facts.IdleCompactSkipReason() != "" {
		return facts, true, nil
	}
	if err := s.addWorkerFacts(ctx, rec, now, &facts); err != nil {
		return facts, false, err
	}
	return facts, true, nil
}

// attemptUnsettled marks an attempt whose turn has not ended yet.
const attemptUnsettled int64 = -1

// attempted reports whether an idle compaction was already attempted with no
// real turn since. The first idle read after an attempt records the context
// size it left; a different size later means a turn ran and re-arms it.
func (s *Sweeper) attempted(id domain.SessionID, state ChatState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	after, ok := s.attempts[id]
	switch {
	case !ok:
		return false
	case after == attemptUnsettled:
		if !state.Busy {
			s.attempts[id] = state.ContextUsed
		}
		return true
	case after == state.ContextUsed:
		return true
	}
	delete(s.attempts, id)
	return false
}

// addWorkerFacts adds the reports, workers and reviews routed to orch: every
// worker whose lane resolves to it, as report delivery routes them
// (domain.PickOrchestrator).
func (s *Sweeper) addWorkerFacts(ctx context.Context, orch domain.SessionRecord, now time.Time, facts *domain.IdleCompactFacts) error {
	recs, err := s.deps.Store.ListSessions(ctx, orch.ProjectID)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	var lanes map[domain.SessionID]string
	if ls, ok := s.deps.Store.(LaneStore); ok {
		if lanes, err = ls.ListSessionLanes(ctx, orch.ProjectID); err != nil {
			return fmt.Errorf("list lanes: %w", err)
		}
	}
	for _, w := range recs {
		if w.Kind != domain.KindWorker {
			continue
		}
		if target, ok := domain.PickOrchestrator(recs, lanes, lanes[w.ID]); !ok || target.ID != orch.ID {
			continue
		}
		reports, err := s.deps.Store.ListReportsBySession(ctx, w.ID)
		if err != nil {
			return fmt.Errorf("list reports of %s: %w", w.ID, err)
		}
		for _, r := range reports {
			if r.DeliveryState == domain.ReportPending || r.DeliveryState == domain.ReportClaimed {
				facts.PendingReports++
			}
		}
		if w.IsTerminated || w.Activity.State == domain.ActivityExited {
			continue
		}
		if len(reports) == 0 || reports[len(reports)-1].State != domain.ReportDone {
			if now.Sub(w.Activity.LastActivityAt) > domain.IdleCompactStalledWorker {
				s.logStalled(orch.ID, w)
			} else {
				facts.InFlightWorkers = append(facts.InFlightWorkers, w.ID)
			}
		}
		runs, err := s.deps.Store.ListRunningReviewRunsBySession(ctx, w.ID)
		if err != nil {
			return fmt.Errorf("list running reviews of %s: %w", w.ID, err)
		}
		facts.RunningReviews += len(runs)
	}
	return nil
}

// logStalled notes, once per quiet spell, a worker with an open task that no
// longer holds idle compaction back, so its orchestrator can follow it up.
func (s *Sweeper) logStalled(orch domain.SessionID, w domain.SessionRecord) {
	s.mu.Lock()
	seen, ok := s.stalled[w.ID]
	s.stalled[w.ID] = w.Activity.LastActivityAt
	s.mu.Unlock()
	if ok && seen.Equal(w.Activity.LastActivityAt) {
		return
	}
	s.deps.Log.Info("idle compaction: worker with an open task has been quiet; not waiting for it",
		"orchestrator", orch, "worker", w.ID, "lastActivity", w.Activity.LastActivityAt)
}
