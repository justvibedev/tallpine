// Package diag is the daemon's self-diagnostics: a handful of process-wide
// counters fed by the HTTP layer and the CDC broadcaster, plus a once-a-minute
// "daemon metrics" log line. It exists so the next hang leaves evidence
// (goroutines, FDs, RSS, heap, SSE clients, p95 handler latency, CDC rate) in
// the daemon log instead of having to be reconstructed from macOS reports.
// Nothing here leaves the machine; it is a local log line, not telemetry.
package diag

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tallpine.eu/tallpine/backend/internal/process"
)

// Interval is how often the metrics line is logged.
const Interval = time.Minute

// maxLatencySamples bounds the per-interval latency buffer; beyond it samples
// are dropped (the p95 of the first N is still a usable signal).
const maxLatencySamples = 20000

var (
	sseClients atomic.Int64
	cdcEvents  atomic.Uint64

	latMu   sync.Mutex
	latency []time.Duration
)

// SSEClientOpened and SSEClientClosed track live event-stream subscribers.
func SSEClientOpened() { sseClients.Add(1) }

// SSEClientClosed pairs with SSEClientOpened.
func SSEClientClosed() { sseClients.Add(-1) }

// CDCEvent counts one change event delivered by the CDC poller.
func CDCEvent() { cdcEvents.Add(1) }

// ObserveRequest records the duration of one bounded request. Long-lived
// streams must not be recorded: their duration is the connection lifetime.
func ObserveRequest(d time.Duration) {
	latMu.Lock()
	if len(latency) < maxLatencySamples {
		latency = append(latency, d)
	}
	latMu.Unlock()
}

// drainLatency returns the request count and p95 since the last drain.
func drainLatency() (count int, p95 time.Duration) {
	latMu.Lock()
	samples := latency
	latency = nil
	latMu.Unlock()
	if len(samples) == 0 {
		return 0, 0
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	// Nearest-rank p95.
	idx := (len(samples)*95 + 99) / 100
	return len(samples), samples[idx-1]
}

// Run logs one "daemon metrics" line per interval until ctx is cancelled.
func Run(ctx context.Context, log *slog.Logger, interval time.Duration) {
	if interval <= 0 {
		interval = Interval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	last := time.Now()
	lastCDC := cdcEvents.Load()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			cdcNow := cdcEvents.Load()
			log.Info("daemon metrics", metricAttrs(now.Sub(last), cdcNow-lastCDC)...)
			last, lastCDC = now, cdcNow
		}
	}
}

func metricAttrs(elapsed time.Duration, cdcDelta uint64) []any {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	requests, p95 := drainLatency()
	cdcRate := 0.0
	if secs := elapsed.Seconds(); secs > 0 {
		cdcRate = float64(cdcDelta) / secs
	}
	return []any{
		"goroutines", runtime.NumGoroutine(),
		"fds", openFDs(),
		"rss_mb", mb(rssBytes()),
		"heap_alloc_mb", ms.HeapAlloc >> 20,
		"heap_inuse_mb", ms.HeapInuse >> 20,
		"gc_pause_total_ms", ms.PauseTotalNs / uint64(time.Millisecond),
		"sse_clients", sseClients.Load(),
		"http_requests", requests,
		"http_p95_ms", float64(p95.Microseconds()) / 1000,
		"cdc_events_per_s", float64(int(cdcRate*100)) / 100,
	}
}

// mb converts bytes to whole megabytes; a negative input means "unknown".
func mb(b int64) int64 {
	if b < 0 {
		return -1
	}
	return b >> 20
}

// openFDs counts this process's open file descriptors, or -1 where unknown.
func openFDs() int {
	var dir string
	switch runtime.GOOS {
	case "linux":
		dir = "/proc/self/fd"
	case "darwin":
		dir = "/dev/fd"
	default:
		return -1
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return -1
	}
	return len(entries)
}

// rssBytes returns the resident set size, or -1 where unknown. macOS has no
// /proc, and a cgo-free task_info is not available, so one cheap `ps` per
// interval is used there.
func rssBytes() int64 {
	switch runtime.GOOS {
	case "linux":
		data, err := os.ReadFile("/proc/self/statm")
		if err != nil {
			return -1
		}
		fields := strings.Fields(string(data))
		if len(fields) < 2 {
			return -1
		}
		pages, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return -1
		}
		return pages * int64(os.Getpagesize())
	case "darwin":
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		out, err := process.CommandContext(ctx, "ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
		if err != nil {
			return -1
		}
		kb, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		if err != nil {
			return -1
		}
		return kb << 10
	}
	return -1
}
