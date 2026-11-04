package terminal

import (
	"context"
	"encoding/base64"
	"sync"
	"time"
)

// outQueue is the per-connection write queue. A buffered channel cannot serve
// both frame classes: control frames must never be dropped or block their
// caller, while PTY output must push back on its own read loop.
type outQueue struct {
	mu     sync.Mutex
	frames []serverMsg
	bytes  int
	wake   chan struct{}
	room   chan struct{}
}

func newOutQueue() *outQueue {
	return &outQueue{wake: make(chan struct{}, 1), room: make(chan struct{}, 1)}
}

// maxMergedData bounds one merged data frame.
const maxMergedData = 256 << 10

func (q *outQueue) push(msg serverMsg) {
	q.mu.Lock()
	// PTY output arrives in many small reads. Output queued behind the writer
	// for the same terminal joins the frame before it, so a burst is sent as a
	// few messages instead of thousands of tiny ones (each costing an envelope,
	// a base64 pass and a renderer write).
	if n := len(q.frames); n > 0 && msg.raw != nil {
		if last := &q.frames[n-1]; last.raw != nil && last.Ch == msg.Ch && last.ID == msg.ID &&
			last.Type == msg.Type && len(last.raw)+len(msg.raw) <= maxMergedData {
			last.raw = append(last.raw, msg.raw...)
			q.bytes += len(msg.raw)
			q.mu.Unlock()
			return
		}
	}
	if msg.raw != nil {
		msg.raw = append([]byte(nil), msg.raw...)
	}
	q.frames = append(q.frames, msg)
	q.bytes += len(msg.Data) + len(msg.raw)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *outQueue) empty() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.frames) == 0
}

func (q *outQueue) full() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.bytes >= dataWatermark
}

func (q *outQueue) drain() []serverMsg {
	q.mu.Lock()
	frames := q.frames
	q.frames, q.bytes = nil, 0
	q.mu.Unlock()
	for i := range frames {
		if frames[i].raw != nil {
			frames[i].Data = base64.StdEncoding.EncodeToString(frames[i].raw)
			frames[i].raw = nil
		}
	}
	select {
	case q.room <- struct{}{}:
	default:
	}
	return frames
}

// enqueue pushes a frame to the writer. Data frames wait for room so the
// attachment's PTY read loop stalls and tmux throttles the producer; control
// frames never wait, because the CDC fan-out enqueues on one goroutine shared
// by every connection.
func (c *connState) enqueue(msg serverMsg) {
	for msg.Type == msgData && c.out.full() {
		select {
		case <-c.out.room:
		case <-c.ctx.Done():
			return
		}
	}
	c.out.push(msg)
}

func (c *connState) writeLoop(ctx context.Context, flushInterval time.Duration) {
	var lastWrite time.Time
	var pace *time.Timer
	defer func() {
		if pace != nil {
			pace.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.out.wake:
		}
		// A wake left over from frames an earlier drain already took has nothing
		// to pace or send.
		if c.out.empty() {
			continue
		}
		if wait := flushInterval - time.Since(lastWrite); wait > 0 {
			if pace == nil {
				pace = time.NewTimer(wait)
			} else {
				pace.Reset(wait)
			}
			select {
			case <-ctx.Done():
				return
			case <-pace.C:
			}
		}
		msgs := c.out.drain()
		if len(msgs) == 0 {
			// Not a write; counting it would hold back the next isolated frame.
			continue
		}
		for _, msg := range msgs {
			if err := c.conn.WriteJSON(ctx, msg); err != nil {
				c.cancel()
				return
			}
		}
		lastWrite = time.Now()
	}
}

func (c *connState) heartbeatLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, interval)
			err := c.conn.Ping(pctx)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		}
	}
}
