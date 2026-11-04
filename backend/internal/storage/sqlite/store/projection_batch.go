package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// Streaming Chat sessions each commit a provider-event projection (archive row
// plus the message, activity or turn update it implies) every flush interval.
// At thirty streaming agents that was hundreds of single-unit transactions a
// second, each paying its own BEGIN, COMMIT and WAL write under the one writer.
// The batcher commits the projections that are waiting together: one
// transaction, each unit inside its own savepoint so a failing unit rolls back
// alone and reports its own error, exactly as if it had committed by itself.
const (
	// projectionBatchMax bounds how long one batch can hold the writer.
	//
	// A unit is durable only once its batch commits. For a journaled
	// (persistent-host) provider nothing changes: events are acknowledged after
	// that commit, so a crash replays them. For an unjournaled provider a crash
	// loses what had not committed yet, as it always did; batching only widens
	// that window by the units sharing the batch, each already waiting at most
	// one flush tick.
	projectionBatchMax = 64
	// projectionLinger is how long a batch that opens while sessions are
	// streaming waits for the projections of the same flush tick to arrive.
	// Streams flush on a shared tick (see the chat delta pump), so they land
	// within a fraction of this.
	projectionLinger = time.Millisecond
	// projectionStreamingWindow decides whether a projection arrives while
	// others are streaming. An idle session's lone event never lingers.
	projectionStreamingWindow = 100 * time.Millisecond
)

// projectionBatchWatchdog bounds a whole multi-unit batch. Past it the batch's
// statements are interrupted, the transaction rolls back, and every unit gets
// one transaction of its own, so one stuck unit cannot hold the writer for
// every session. There is deliberately no per-unit deadline: a unit's deadline
// firing mid-statement would interrupt the shared transaction just the same.
// A variable so tests can shorten it.
var projectionBatchWatchdog = 30 * time.Second

// batchUnitContext gives a unit its caller's values but the batch's
// cancellation: a caller that stops or hibernates cannot interrupt the shared
// transaction (the driver answers a done context with sqlite3_interrupt, which
// rolls back the whole transaction); only the batch watchdog can.
type batchUnitContext struct {
	context.Context // the batch: Deadline, Done, Err
	caller          context.Context
}

func (c batchUnitContext) Value(key any) any { return c.caller.Value(key) }

type projectionBatcher struct {
	mu      sync.Mutex
	queue   []*projectionRequest
	leading bool
	lastAt  time.Time
}

type projectionRequest struct {
	ctx       context.Context
	run       func(context.Context, *sql.Tx, *gen.Queries) (bool, error)
	projected bool
	err       error
	// batchFailed marks an error that came from the shared transaction, not
	// from the unit itself; such a unit is retried once on its own.
	batchFailed bool
	// interrupted marks the unit the batch watchdog stopped: it is retried
	// last, under the watchdog as its own deadline.
	interrupted bool
	answered    bool
	// done is closed once the unit's transaction finished either way.
	done chan struct{}
	// lead is closed when this waiting caller takes over running batches.
	lead chan struct{}
}

// groupCommitProjection runs one projection unit in a transaction shared with
// whatever other units are waiting, and reports the unit's own outcome after
// that transaction committed or rolled back.
//
// No goroutine is dedicated to the batcher: the first caller leads one batch,
// which includes its own unit, and then hands leadership to the oldest caller
// still waiting, so no session's pump is kept running other sessions' work.
func (s *Store) groupCommitProjection(ctx context.Context, run func(context.Context, *sql.Tx, *gen.Queries) (bool, error)) (bool, error) {
	req := &projectionRequest{ctx: ctx, run: run, done: make(chan struct{}), lead: make(chan struct{})}
	b := &s.projections
	b.mu.Lock()
	now := time.Now()
	streaming := !b.lastAt.IsZero() && now.Sub(b.lastAt) < projectionStreamingWindow
	b.lastAt = now
	b.queue = append(b.queue, req)
	leader := !b.leading
	b.leading = true
	b.mu.Unlock()
	if leader {
		if streaming {
			time.Sleep(projectionLinger)
		}
		s.leadProjectionBatch()
	} else {
		select {
		case <-req.done:
		case <-req.lead:
			s.leadProjectionBatch()
		}
	}
	<-req.done
	return req.projected, req.err
}

// leadProjectionBatch commits the oldest waiting units, then passes leadership
// on, or ends it when nothing is waiting.
func (s *Store) leadProjectionBatch() {
	b := &s.projections
	b.mu.Lock()
	n := min(len(b.queue), projectionBatchMax)
	batch := append([]*projectionRequest(nil), b.queue[:n]...)
	b.queue = append(b.queue[:0], b.queue[n:]...)
	b.mu.Unlock()

	answer := func(req *projectionRequest) {
		if !req.answered {
			req.answered = true
			close(req.done)
		}
	}
	// Whatever happens in the batch, every caller in it gets an answer and
	// leadership moves on: a stuck leader would stall every session's Chat
	// persistence, not just the one whose unit failed.
	defer func() {
		if r := recover(); r != nil {
			slog.Error("provider event projection batch panicked", "panic", r, "stack", string(debug.Stack()))
			err := fmt.Errorf("provider event projection batch panicked: %v", r)
			for _, req := range batch {
				if !req.answered && req.err == nil {
					req.projected, req.err = false, err
				}
			}
		}
		for _, req := range batch {
			answer(req)
		}
		b.mu.Lock()
		if len(b.queue) == 0 {
			b.leading = false
			b.mu.Unlock()
			return
		}
		next := b.queue[0]
		b.mu.Unlock()
		close(next.lead)
	}()
	s.commitProjectionBatch(batch)
	if len(batch) == 1 {
		return
	}
	// A failure of the shared transaction says nothing about the other units
	// in it. Each gets one transaction of its own, and is answered as soon as
	// that commits. The unit the watchdog interrupted goes last, under the
	// watchdog as a deadline (alone, a deadline can only roll back its own
	// work), so a unit that stays slow cannot hold the writer ahead of its
	// peers or indefinitely.
	var culprits []*projectionRequest
	for _, req := range batch {
		switch {
		case !req.batchFailed:
			answer(req)
		case req.interrupted:
			culprits = append(culprits, req)
		default:
			req.projected, req.err, req.batchFailed = false, nil, false
			s.commitProjectionBatch([]*projectionRequest{req})
			answer(req)
		}
	}
	for _, req := range culprits {
		req.projected, req.err, req.batchFailed = false, nil, false
		ctx, cancel := context.WithTimeout(req.ctx, projectionBatchWatchdog)
		req.ctx = ctx
		s.commitProjectionBatch([]*projectionRequest{req})
		cancel()
		answer(req)
	}
}

// runProjectionUnit runs one unit and turns a panic into that unit's error, so
// a fault in one session's projection rolls back only its own savepoint.
func runProjectionUnit(ctx context.Context, req *projectionRequest, tx *sql.Tx, q *gen.Queries) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("provider event projection panicked", "panic", r, "stack", string(debug.Stack()))
			req.projected, req.err = false, fmt.Errorf("provider event projection panicked: %v", r)
		}
	}()
	req.projected, req.err = req.run(ctx, tx, q)
}

func (s *Store) commitProjectionBatch(batch []*projectionRequest) {
	fail := func(err error) {
		for _, req := range batch {
			if req.err == nil {
				req.projected, req.err, req.batchFailed = false, err, true
			}
		}
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	// The batch outlives any one caller's context. A multi-unit batch runs its
	// statements under the batch watchdog instead (see batchUnitContext).
	ctx := context.Background()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		fail(fmt.Errorf("begin provider event projection: %w", err))
		return
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	if len(batch) == 1 {
		// Alone in its transaction, a unit keeps its caller's context: a
		// cancellation can only roll back its own work.
		req := batch[0]
		if req.err = req.ctx.Err(); req.err != nil {
			return
		}
		runProjectionUnit(req.ctx, req, tx, q)
		if req.err != nil {
			return
		}
	} else {
		batchCtx, cancel := context.WithTimeout(ctx, projectionBatchWatchdog)
		defer cancel()
		for i, req := range batch {
			// A caller that already gave up fails alone, before touching the
			// shared transaction.
			if req.err = req.ctx.Err(); req.err != nil {
				continue
			}
			savepoint := "projection_" + strconv.Itoa(i)
			if _, err := tx.ExecContext(batchCtx, "SAVEPOINT "+savepoint); err != nil {
				fail(fmt.Errorf("open provider event savepoint: %w", err))
				return
			}
			// Once started, a unit runs without its caller's cancellation: the
			// driver answers a cancelled context with sqlite3_interrupt, which
			// rolls back the whole shared transaction, every other session's
			// units included. Only the batch watchdog can interrupt it.
			runProjectionUnit(batchUnitContext{Context: batchCtx, caller: req.ctx}, req, tx, q)
			if req.err != nil {
				req.projected = false
				if batchCtx.Err() != nil {
					// The watchdog interrupted this unit; it gets its own retry
					// after every other unit of the abandoned batch.
					req.batchFailed, req.interrupted = true, true
					fail(fmt.Errorf("provider event projection batch exceeded %s: %w", projectionBatchWatchdog, batchCtx.Err()))
					return
				}
				if _, err := tx.ExecContext(batchCtx, "ROLLBACK TO "+savepoint); err != nil {
					// The transaction can no longer tell this unit's writes from
					// the others': roll everything back and let each caller retry.
					fail(fmt.Errorf("roll back provider event savepoint: %w", err))
					return
				}
			}
			if _, err := tx.ExecContext(batchCtx, "RELEASE "+savepoint); err != nil {
				fail(fmt.Errorf("release provider event savepoint: %w", err))
				return
			}
		}
	}
	if err := tx.Commit(); err != nil {
		fail(fmt.Errorf("commit provider event projection: %w", err))
	}
}
