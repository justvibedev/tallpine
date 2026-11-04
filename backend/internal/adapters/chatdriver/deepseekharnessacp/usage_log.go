package deepseekharnessacp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/klauspost/compress/zstd"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// dsh's ACP stream carries only context occupancy (usage_update used/size) and
// a bare stopReason, so token accounting comes from the session's own log. Each
// model request is committed as one `assistant/message` record whose top-level
// `usage` is the request's accounting; the same numbers repeat inside the
// record's `stream` chunks, which are never read. Of the usage fields,
// inputTokens is the cache-miss count and cacheReadTokens the cache-hit count.
type logRecord struct {
	Type string `json:"type"`
	Seq  int64  `json:"seq"`
	Time int64  `json:"time"`
	Data struct {
		Message struct {
			Source struct {
				Model string `json:"model"`
			} `json:"source"`
		} `json:"message"`
		Usage *struct {
			InputTokens     int64 `json:"inputTokens"`
			OutputTokens    int64 `json:"outputTokens"`
			CacheReadTokens int64 `json:"cacheReadTokens"`
		} `json:"usage"`
	} `json:"data"`
}

var (
	// maxFrameBytes bounds a compressed frame that is buffered; a larger one is
	// skipped unread. dsh appends small per-batch frames.
	maxFrameBytes = 8 << 20
	// maxDecodedBytes bounds one frame's decompressed size, so a hostile or
	// corrupt frame cannot expand without limit.
	maxDecodedBytes uint64 = 64 << 20
)

// ledger is one dsh session's running totals. refresh decodes only the frames
// appended since the last call, one at a time, so a long session costs time
// proportional to its new records and memory proportional to one frame.
type ledger struct {
	info    os.FileInfo // identity of the file the offset belongs to
	offset  int64       // compressed bytes already processed, ending on a frame boundary
	lastSeq int64       // records at or below it were already counted

	requests          int
	miss, hit, output int64
	cost              float64
	unpriced          int // requests whose model has no known price
}

func (l *ledger) reset() { *l = ledger{} }

// refresh folds new log records into the totals. A replaced or shrunk file
// (rotation, torn-tail repair, a format migration) invalidates the offset, so
// the ledger starts over from the top of the new file. A frame that is too large
// or will not decode is skipped and the offset moves past it, so it is never
// retried; unreadable framing stops the read where it is.
func (l *ledger) refresh(path string, log *slog.Logger) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if l.info != nil && (!os.SameFile(l.info, st) || st.Size() < l.offset) {
		l.reset()
	}
	l.info = st
	if st.Size() == l.offset {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	dec, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(maxDecodedBytes), zstd.WithDecoderConcurrency(1))
	if err != nil {
		return err
	}
	defer dec.Close()

	r := bufio.NewReaderSize(io.NewSectionReader(f, l.offset, st.Size()-l.offset), 256<<10)
	for {
		frame, size, err := readFrame(r, maxFrameBytes)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil // caught up, or a frame still being written
		}
		if errors.Is(err, errBadFrame) {
			l.reset() // a file rewritten in place: recount from the top next time
			return err
		}
		if err != nil {
			return err
		}
		if frame == nil {
			debugf(log, "deepseek-harness usage frame too large; skipped", "bytes", size)
		} else if plain, err := dec.DecodeAll(frame, nil); err != nil {
			debugf(log, "deepseek-harness usage frame undecodable; skipped", "error", err)
		} else {
			l.addRecords(plain)
		}
		l.offset += size
	}
}

func debugf(log *slog.Logger, msg string, args ...any) {
	if log != nil {
		log.Debug(msg, args...)
	}
}

func (l *ledger) addRecords(plain []byte) {
	for len(plain) > 0 {
		line := plain
		if i := bytes.IndexByte(plain, '\n'); i >= 0 {
			line, plain = plain[:i], plain[i+1:]
		} else {
			plain = nil
		}
		// Cheap prefilter: most records are not assistant messages and some are large.
		if !bytes.Contains(line, []byte(`"type":"assistant/message"`)) {
			continue
		}
		var rec logRecord
		if json.Unmarshal(line, &rec) != nil || rec.Type != "assistant/message" ||
			rec.Data.Usage == nil || rec.Seq <= l.lastSeq {
			continue
		}
		l.lastSeq = rec.Seq
		u := rec.Data.Usage
		l.requests++
		l.miss += u.InputTokens
		l.hit += u.CacheReadTokens
		l.output += u.OutputTokens
		if c, ok := requestCost(rec.Data.Message.Source.Model, time.UnixMilli(rec.Time), u.InputTokens, u.CacheReadTokens, u.OutputTokens); ok {
			l.cost += c
		} else {
			l.unpriced++
		}
	}
}

// usage is the session's cumulative accounting. Cost is reported only while
// every request had a known price; once one did not, CostUnknown withdraws the
// figure reported earlier instead of leaving a stale, understated one.
func (l *ledger) usage() *ports.ChatUsage {
	if l.requests == 0 {
		return nil
	}
	u := &ports.ChatUsage{
		InputTokens: l.miss, CachedTokens: l.hit, OutputTokens: l.output,
		TotalTokens: l.miss + l.hit + l.output, TotalsKnown: true,
	}
	if l.unpriced > 0 {
		u.CostUnknown = true
	} else {
		cost := l.cost
		u.Cost, u.Currency = &cost, "USD"
	}
	return u
}
