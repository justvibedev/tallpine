package diag

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestDrainLatencyP95(t *testing.T) {
	drainLatency() // reset shared state
	for i := 1; i <= 100; i++ {
		ObserveRequest(time.Duration(i) * time.Millisecond)
	}
	n, p95 := drainLatency()
	if n != 100 || p95 != 95*time.Millisecond {
		t.Fatalf("count=%d p95=%v, want 100 and 95ms", n, p95)
	}
	if n, p95 := drainLatency(); n != 0 || p95 != 0 {
		t.Fatalf("second drain = %d %v, want empty", n, p95)
	}
}

func TestMetricsLineCarriesEveryField(t *testing.T) {
	drainLatency()
	SSEClientOpened()
	defer SSEClientClosed()
	ObserveRequest(10 * time.Millisecond)

	var buf syncBuffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Run(ctx, log, 10*time.Millisecond); close(done) }()
	deadline := time.After(5 * time.Second)
	for !strings.Contains(buf.String(), "daemon metrics") {
		select {
		case <-deadline:
			t.Fatal("no metrics line logged")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
	line := buf.String()
	t.Log(strings.TrimSpace(line))
	for _, key := range []string{"goroutines=", "fds=", "rss_mb=", "heap_alloc_mb=", "heap_inuse_mb=", "sse_clients=", "http_p95_ms=", "cdc_events_per_s="} {
		if !strings.Contains(line, key) {
			t.Errorf("metrics line missing %s: %s", key, line)
		}
	}
}
