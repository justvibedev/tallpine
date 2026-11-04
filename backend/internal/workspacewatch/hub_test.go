package workspacewatch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeBackends replaces the OS watcher with channels the test drives.
type fakeBackends struct {
	mu      sync.Mutex
	started int
	sources []chan struct{}
	stops   []func()
	ctxs    []context.Context
	err     error
}

func installFakeBackends(t *testing.T) (*hub, *fakeBackends) {
	t.Helper()
	fake := &fakeBackends{}
	previous := startRootWatch
	startRootWatch = func(ctx context.Context, _ string) (<-chan struct{}, error) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if fake.err != nil {
			return nil, fake.err
		}
		fake.started++
		source := make(chan struct{}, 1)
		stop := sync.OnceFunc(func() { close(source) })
		fake.sources = append(fake.sources, source)
		fake.stops = append(fake.stops, stop)
		fake.ctxs = append(fake.ctxs, ctx)
		go func() {
			<-ctx.Done()
			stop()
		}()
		return source, nil
	}
	t.Cleanup(func() { startRootWatch = previous })
	return &hub{roots: map[string]*rootWatch{}}, fake
}

func (f *fakeBackends) snapshot() (int, []chan struct{}, []context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started, append([]chan struct{}(nil), f.sources...), append([]context.Context(nil), f.ctxs...)
}

func receive(t *testing.T, changes <-chan struct{}) {
	t.Helper()
	select {
	case _, ok := <-changes:
		if !ok {
			t.Fatal("subscription closed, want a change")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no change delivered")
	}
}

func waitClosed(t *testing.T, changes <-chan struct{}) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-changes:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("subscription was not closed")
		}
	}
}

func TestHubSharesOneBackendPerRootAndStopsWithTheLastViewer(t *testing.T) {
	h, fake := installFakeBackends(t)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	first, err := h.subscribe(firstCtx, "/ws/a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.subscribe(secondCtx, "/ws/a")
	if err != nil {
		t.Fatal(err)
	}
	started, sources, ctxs := fake.snapshot()
	if started != 1 {
		t.Fatalf("two viewers of one root started %d backends, want 1", started)
	}
	sources[0] <- struct{}{}
	receive(t, first)
	receive(t, second)

	cancelFirst()
	waitClosed(t, first)
	if ctxs[0].Err() != nil {
		t.Fatal("backend stopped while a viewer remained")
	}
	sources[0] <- struct{}{}
	receive(t, second)

	cancelSecond()
	waitClosed(t, second)
	select {
	case <-ctxs[0].Done():
	case <-time.After(2 * time.Second):
		t.Fatal("backend kept running after the last viewer left")
	}
	if n := h.watching(); n != 0 {
		t.Fatalf("hub still tracks %d roots, want 0", n)
	}
}

func TestHubStartsAFreshBackendAfterTheLastViewerLeft(t *testing.T) {
	h, fake := installFakeBackends(t)
	ctx, cancel := context.WithCancel(context.Background())
	first, err := h.subscribe(ctx, "/ws/a")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	waitClosed(t, first)
	again, err := h.subscribe(context.Background(), "/ws/a")
	if err != nil {
		t.Fatal(err)
	}
	started, sources, _ := fake.snapshot()
	if started != 2 {
		t.Fatalf("backends started = %d, want a fresh one", started)
	}
	sources[1] <- struct{}{}
	receive(t, again)
}

func TestHubClosesViewersWhenTheBackendStops(t *testing.T) {
	h, fake := installFakeBackends(t)
	changes, err := h.subscribe(context.Background(), "/ws/a")
	if err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	stop := fake.stops[0]
	fake.mu.Unlock()
	stop()
	waitClosed(t, changes)
	if n := h.watching(); n != 0 {
		t.Fatalf("hub still tracks %d roots after its backend stopped", n)
	}
}

func TestHubReportsBackendFailureToEveryJoiner(t *testing.T) {
	h, fake := installFakeBackends(t)
	fake.err = errors.New("no watcher")
	if _, err := h.subscribe(context.Background(), "/ws/a"); err == nil {
		t.Fatal("subscribe succeeded without a backend")
	}
	if n := h.watching(); n != 0 {
		t.Fatalf("a failed start left %d roots registered", n)
	}
}

func TestHubCancelledJoinerDoesNotKeepTheBackendAlive(t *testing.T) {
	h, fake := installFakeBackends(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.subscribe(ctx, "/ws/a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled subscribe err = %v", err)
	}
	_, _, ctxs := fake.snapshot()
	for _, backendCtx := range ctxs {
		select {
		case <-backendCtx.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("backend started for a cancelled joiner kept running")
		}
	}
	if n := h.watching(); n != 0 {
		t.Fatalf("hub tracks %d roots with no viewer", n)
	}
}
