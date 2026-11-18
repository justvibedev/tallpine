package turnnotify

import (
	"context"
	"regexp"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// waitTextGrace bounds how long a last message saying "waiting for my lint"
// holds a turn_ended back when no live job confirms it.
const waitTextGrace = 10 * time.Minute

// waitingOnOwnJob matches a last message where the worker says it waits on
// its own background job or check.
// needsOrchestrator marks a message that waits on someone else ("your review").
var needsOrchestrator = regexp.MustCompile(`(?i)\byour\b`)

var waitingOnOwnJob = regexp.MustCompile(`(?i)\b(waiting|awaiting|waits)\b[^.\n]{0,40}\b(lint|tests?|build|ci|background|job|checks?)\b`)

// holdState is a pending turn_ended's hold: since when it is held, whether a
// live job caused it, and whether that job has ended so one more quiet
// IdleDelay runs before the notice goes out.
type holdState struct {
	since     time.Time
	onJob     bool
	releasing bool
}

// holdForOwnJob keeps a plain turn_ended back while the worker has a live
// background job (checked every NudgePoll up to NudgeMaxWait, then one more IdleDelay), or its last
// message says it is waiting on its own job (up to waitTextGrace). The report
// then surfaces only if the job finishes and the worker still said nothing,
// because the re-armed timer sends it once nothing holds it. Caller holds o.mu
// and t is the current turn of id.
func (o *Observer) holdForOwnJob(id domain.SessionID, t *turn, gen uint64) bool {
	// A stop the worker did not choose is never waiting on its own job.
	if kind, _ := turnEndKind(t.last); !t.limitUntil.IsZero() || kind == KindContextThrash {
		return false
	}
	o.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	jobs := 0
	if o.Jobs != nil {
		if rec, ok, err := o.Sessions.GetSession(ctx, id); err == nil && ok && rec.Kind == domain.KindWorker {
			if n, jerr := o.Jobs.RunningJobs(ctx, rec); jerr == nil {
				jobs = n
			}
		}
	}
	cancel()
	o.mu.Lock()
	if o.closed || o.turns[id] != t || t.gen != gen || t.finalReported || t.notified {
		return true // superseded while unlocked; nothing to send
	}
	var limit time.Duration
	switch {
	case jobs > 0:
		limit, t.hold.onJob = o.NudgeMaxWait, true
	case t.hold.onJob && !t.hold.releasing:
		// The job ended: give the worker one more quiet window to follow up
		// (resume, report) before the orchestrator hears of a silent turn.
		t.hold.releasing = true
		t.timer = o.AfterFunc(o.IdleDelay, func() { o.idleElapsed(id, gen) })
		return true
	case t.hold.onJob:
		return false
	case waitingOnOwnJob.MatchString(t.last) && !needsOrchestrator.MatchString(t.last):
		limit = waitTextGrace
	default:
		return false
	}
	now := o.Now()
	if t.hold.since.IsZero() {
		t.hold.since = now
	}
	if now.Sub(t.hold.since) >= limit {
		return false
	}
	t.timer = o.AfterFunc(o.NudgePoll, func() { o.idleElapsed(id, gen) })
	return true
}

// holdChatFallback keeps a chat worker's plain turn_ended back while the worker
// still runs its own background job, as holdForOwnJob does for a TUI turn
// (B799): a chat turn's notice comes from its durable row, which skipped that
// hold, so the orchestrator read "went idle" for a worker that was only
// waiting on its test run. The job's completion wakes the worker, and that new
// turn retires the row. Once the job ends the notice waits one more IdleDelay
// for that wake-up turn. It returns how long to wait before checking again,
// or 0 to deliver now; a job outliving NudgeMaxWait no longer holds it.
func (o *Observer) holdChatFallback(ctx context.Context, f domain.TurnFallback, rec domain.SessionRecord, kind string) time.Duration {
	if o.Jobs == nil || kind != KindTurnEnded || rec.Kind != domain.KindWorker {
		return 0
	}
	now := o.Now()
	if now.Sub(f.CompletedAt) >= o.NudgeMaxWait {
		return 0
	}
	n, err := o.Jobs.RunningJobs(ctx, rec)
	o.mu.Lock()
	defer o.mu.Unlock()
	if err == nil && n > 0 {
		if o.jobHolds == nil {
			o.jobHolds = map[string]time.Time{}
		}
		o.jobHolds[f.ID] = now
		return o.NudgePoll
	}
	if seen, held := o.jobHolds[f.ID]; held && now.Sub(seen) < o.IdleDelay {
		return o.IdleDelay - now.Sub(seen)
	}
	return 0
}

// pruneJobHolds forgets holds of rows no longer pending (delivered, or retired
// by the worker's next turn).
func (o *Observer) pruneJobHolds(pending []domain.TurnFallback) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.jobHolds) == 0 {
		return
	}
	live := make(map[string]bool, len(pending))
	for _, f := range pending {
		live[f.ID] = true
	}
	for id := range o.jobHolds {
		if !live[id] {
			delete(o.jobHolds, id)
		}
	}
}
