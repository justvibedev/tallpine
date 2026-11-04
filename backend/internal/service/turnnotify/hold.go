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
	if !t.limitUntil.IsZero() {
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
