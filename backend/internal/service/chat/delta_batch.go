package chat

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// deltaFlushInterval is the longest a streamed delta waits for its write while
// more of the same stream keeps arriving. A var so tests can change it.
var deltaFlushInterval = 50 * time.Millisecond

// deltaRunMaxBytes and deltaRunMaxEvents bound one run of deltas, and with it
// the archive row and the transaction a backlog can produce. The event bound
// matters for deltas with native identities, which keep an archive row each:
// without it, a replayed backlog of tiny chunks would hold the shared writer
// for one very long transaction.
const (
	deltaRunMaxBytes  = 32 * 1024
	deltaRunMaxEvents = 1024
)

// coalescible reports whether event only appends text to one provider item.
func coalescible(event ports.ChatEvent) bool {
	switch event.Kind {
	case ports.ChatEventMessageDelta, ports.ChatEventReasoningDelta, ports.ChatEventCommandOutputDelta,
		ports.ChatEventCommandInput, ports.ChatEventActivityText:
		// SQLite's capped appends stop at NUL; joining would discard later deltas too.
		return event.Err == nil && !strings.ContainsRune(event.Delta, '\x00')
	}
	return false
}

// sameStream reports whether next appends to the same column of the same row as
// run, carrying the same metadata. Only the text and the native event identity
// may differ.
func sameStream(run, next ports.ChatEvent) bool {
	run.Delta, run.ProviderEventID = "", ""
	next.Delta, next.ProviderEventID = "", ""
	return reflect.DeepEqual(run, next) //nolint:govet // coalescible events have nil Err; compare the rest exactly.
}

// pumpProviderEvents is pumpProviderEventsWith at one fixed interval, without
// activity snapshots joining runs.
func pumpProviderEvents(
	events <-chan ports.ChatEvent,
	flushRequests <-chan chan struct{},
	interval time.Duration,
	project func([]ports.ChatEvent) bool,
) bool {
	return pumpProviderEventsWith(events, flushRequests, func() deltaPolicy {
		return deltaPolicy{interval: interval, activityInterval: interval}
	}, project)
}

// joinsRunAsSnapshot reports whether event is an activity.started snapshot of
// the item a run of streamed activity text belongs to. ACP providers send each
// output chunk as a delta followed by a snapshot of the whole tool call, so
// without this every chunk would end the run it should have joined.
func joinsRunAsSnapshot(run, event ports.ChatEvent) bool {
	return event.Kind == ports.ChatEventActivityStarted && event.Err == nil &&
		run.Kind != ports.ChatEventMessageDelta && event.ProviderItemID != "" &&
		event.ProviderItemID == run.ProviderItemID && event.ProviderTurnID == run.ProviderTurnID &&
		event.ProviderConversationID == run.ProviderConversationID
}

// pumpProviderEventsWith hands provider events to project in arrival order, one
// unit at a time: a single event, or a run of consecutive deltas for the same
// stream, which project commits as one transaction instead of one per token.
// With policy.joinSnapshots, a run of streamed activity text also absorbs the
// activity.started snapshots of its own item.
//
// A run is written before any other event, when the stream ends, when a flush is
// requested, when it reaches its size bounds, and no later than interval after
// its first delta. Once nothing more is queued, a run is written at once if it
// starts a different stream from the last run written or no run was written in
// the last interval: an idle writer adds no latency, and a new item's first
// delta, which creates its row and takes its timeline position, is not held
// back. While one stream keeps going, its runs are written on the next tick of a
// clock shared by every session (multiples of interval), so concurrent streams
// reach the store together and commit in one transaction rather than one each
// (see the store's projection batcher). Deltas that arrive while a write is running wait in the channel and
// join the next run. The interval is policy.interval for assistant message
// deltas and policy.activityInterval for every other stream; the policy is read
// when a run starts. It reports false when project stops consumption.
func pumpProviderEventsWith(
	events <-chan ports.ChatEvent,
	flushRequests <-chan chan struct{},
	policy func() deltaPolicy,
	project func([]ports.ChatEvent) bool,
) bool {
	var (
		run       []ports.ChatEvent
		runBytes  int
		runSince  time.Time
		runDue    time.Time
		lastWrite time.Time
		lastHead  ports.ChatEvent
		runPolicy = policy()
	)
	interval := func() time.Duration {
		if len(run) > 0 && run[0].Kind != ports.ChatEventMessageDelta {
			return runPolicy.activityInterval
		}
		return runPolicy.interval
	}
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	flush := func() bool {
		if len(run) == 0 {
			return true
		}
		unit := run
		run, runBytes = nil, 0
		lastWrite, lastHead = time.Now(), unit[0]
		return project(unit)
	}
	answer := func(done chan struct{}) bool {
		written := flush()
		close(done)
		return written
	}
	for {
		if len(run) > 0 && !time.Now().Before(runDue) {
			if !flush() {
				return false
			}
			continue
		}
		var (
			event ports.ChatEvent
			ok    bool
		)
		select {
		case event, ok = <-events:
		case done := <-flushRequests:
			if !answer(done) {
				return false
			}
			continue
		default:
			if len(run) > 0 && (time.Since(lastWrite) >= interval() || !sameStream(lastHead, run[0])) {
				if !flush() {
					return false
				}
				continue
			}
			var due <-chan time.Time
			if len(run) > 0 {
				timer.Reset(time.Until(runDue))
				due = timer.C
			}
			select {
			case event, ok = <-events:
				timer.Stop()
			case <-due:
				if !flush() {
					return false
				}
				continue
			case done := <-flushRequests:
				timer.Stop()
				if !answer(done) {
					return false
				}
				continue
			}
		}
		if !ok {
			return flush()
		}
		if coalescible(event) && len(run) > 0 && len(run) < deltaRunMaxEvents && sameStream(run[0], event) &&
			runBytes+len(event.Delta) <= deltaRunMaxBytes {
			run = append(run, event)
			runBytes += len(event.Delta)
			continue
		}
		if runPolicy.joinSnapshots && len(run) > 0 && len(run) < deltaRunMaxEvents && joinsRunAsSnapshot(run[0], event) {
			run = append(run, event)
			continue
		}
		if !flush() {
			return false
		}
		if coalescible(event) {
			runPolicy = policy()
			run, runBytes, runSince = []ports.ChatEvent{event}, len(event.Delta), time.Now()
			runDue = sharedTickAfter(runSince, interval())
			continue
		}
		if !project([]ports.ChatEvent{event}) {
			return false
		}
	}
}

// sharedTickAfter returns the first multiple of interval after t, at most
// interval away. The offset is taken from the wall clock, which every session
// shares, and added to t so the deadline keeps t's monotonic reading: a wall
// clock step cannot hold a run back.
func sharedTickAfter(t time.Time, interval time.Duration) time.Time {
	if interval <= 0 {
		return t
	}
	return t.Add(t.Truncate(interval).Add(interval).Sub(t))
}

// flushDeltas asks the projector to write the run it is batching and waits until
// that write has finished, the controller stopped, or ctx ended.
func (c *Controller) flushDeltas(ctx context.Context) {
	done := make(chan struct{})
	select {
	case c.flushRequests <- done:
	case <-c.stopped:
		return
	case <-ctx.Done():
		return
	}
	select {
	case <-done:
	case <-c.stopped:
	case <-ctx.Done():
	}
}

// projectDeltas commits a run of deltas for one stream in one transaction.
//
// Deltas without a native event identity are never deduplicated, so the run is
// archived as one event holding the joined text. Deltas with one keep a row
// apiece: a persistent host replays an unacknowledged prompt from its start, and
// a replay can split the stream into different runs, so only per-event
// identities let the archive skip exactly the deltas it already holds. The first
// delta not yet archived opens the transaction and its row holds the text of the
// whole remaining run; the rest join it as identity-only rows, the '{}' form
// archive compaction reduces settled deltas to anyway, so a run costs one
// encoded payload rather than one per token. Only text the archive had not seen
// is applied.
func (c *Controller) projectDeltas(ctx context.Context, run []ports.ChatEvent) (bool, error) {
	if slices.ContainsFunc(run, isActivitySnapshot) {
		return c.projectRunWithSnapshots(ctx, run)
	}
	merged := run[0]
	if !slices.ContainsFunc(run, func(event ports.ChatEvent) bool { return event.ProviderEventID != "" }) {
		var text strings.Builder
		for _, event := range run {
			text.WriteString(event.Delta)
		}
		merged.Delta = text.String()
		projected, _, err := c.projectEvent(ctx, merged, false)
		return projected, err
	}
	merged.ProviderEventID = ""
	for i, head := range run {
		archived := head
		if len(run[i+1:]) > 0 {
			var all strings.Builder
			for _, event := range run[i:] {
				all.WriteString(event.Delta)
			}
			archived.Delta = all.String()
		}
		projected, err := c.archive(ctx, archived, func(txCtx context.Context) error {
			var text strings.Builder
			text.WriteString(head.Delta)
			for _, next := range run[i+1:] {
				fresh, err := c.archiveIdentity(txCtx, next)
				if err != nil {
					return err
				}
				if fresh {
					text.WriteString(next.Delta)
				}
			}
			merged.Delta = text.String()
			return c.apply(txCtx, merged, false)
		})
		if err != nil || projected {
			return projected, err
		}
	}
	return false, nil
}

// archiveIdentity archives only event's native identity, for a delta whose text
// an earlier row of the same run already holds. It reports whether the
// identity was new.
func (c *Controller) archiveIdentity(ctx context.Context, event ports.ChatEvent) (bool, error) {
	noop := func(context.Context) error { return nil }
	if c.reviewID != "" {
		return c.store.ProjectReviewProviderEvent(ctx, c.conversation.ID, c.sessionID,
			c.reviewID, c.generation, event.ProviderEventID, string(event.Kind), archivedIdentityPayload, c.now(), noop)
	}
	return c.store.ProjectProviderEvent(ctx, c.conversation.ID, c.sessionID,
		c.generation, event.ProviderEventID, string(event.Kind), archivedIdentityPayload, c.now(), noop)
}

// archivedIdentityPayload is the payload of an identity-only archive row, the
// same tombstone archive compaction leaves behind for a settled delta.
const archivedIdentityPayload = "{}"

func isActivitySnapshot(event ports.ChatEvent) bool {
	return event.Kind == ports.ChatEventActivityStarted
}

// projectRunWithSnapshots commits a run of streamed activity text that absorbed
// activity.started snapshots of the same item, in one transaction.
//
// Every event is archived, so each native identity is deduplicated and
// acknowledged exactly as if it had been written alone; deltas without one are
// joined into a single archive event, as in projectDeltas. Only the newest
// snapshot the archive had not seen is applied, because each snapshot replaces
// the whole row, and it is applied before the text so the row exists to append
// to. The text is every fresh delta's, in arrival order.
func (c *Controller) projectRunWithSnapshots(ctx context.Context, run []ports.ChatEvent) (bool, error) {
	joinText := !slices.ContainsFunc(run, func(event ports.ChatEvent) bool {
		return !isActivitySnapshot(event) && event.ProviderEventID != ""
	})
	units := make([]ports.ChatEvent, 0, len(run))
	joined := -1
	for _, event := range run {
		if joinText && !isActivitySnapshot(event) {
			if joined < 0 {
				joined = len(units)
				units = append(units, event)
			} else {
				units[joined].Delta += event.Delta
			}
			continue
		}
		units = append(units, event)
	}
	for i, head := range units {
		projected, err := c.archive(ctx, head, func(txCtx context.Context) error {
			var (
				snapshot *ports.ChatEvent
				delta    *ports.ChatEvent
				text     strings.Builder
			)
			take := func(event ports.ChatEvent) {
				if isActivitySnapshot(event) {
					snapshot = &event
					return
				}
				if delta == nil {
					delta = &event
				}
				text.WriteString(event.Delta)
			}
			take(head)
			for _, next := range units[i+1:] {
				fresh, err := c.archive(txCtx, next, func(context.Context) error { return nil })
				if err != nil {
					return err
				}
				if fresh {
					take(next)
				}
			}
			if snapshot != nil {
				if err := c.apply(txCtx, *snapshot, false); err != nil {
					return err
				}
			}
			if delta == nil || text.Len() == 0 {
				return nil
			}
			merged := *delta
			merged.ProviderEventID = ""
			merged.Delta = text.String()
			return c.apply(txCtx, merged, false)
		})
		if err != nil || projected {
			return projected, err
		}
	}
	return false, nil
}
