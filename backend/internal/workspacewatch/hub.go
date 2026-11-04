package workspacewatch

import (
	"context"
	"sync"
)

// startRootWatch starts the operating-system watcher behind one shared root. A
// variable so tests can count and drive backends.
var startRootWatch = watchRootNative

// shared is the process-wide registry of root watchers.
var shared = &hub{roots: map[string]*rootWatch{}}

// hub hands every subscriber of a workspace root a view of one shared
// watcher. Each workspace SSE stream used to start its own: on macOS that is a
// kqueue descriptor per file of the worktree, thousands per open view, and
// every view repeated the same git discovery and ignore checks. A root's
// watcher starts with its first subscriber and stops with its last.
type hub struct {
	mu    sync.Mutex
	roots map[string]*rootWatch
}

type rootWatch struct {
	root   string
	cancel context.CancelFunc
	// ready is closed once the backend started (or failed with err).
	ready chan struct{}
	err   error

	mu sync.Mutex
	// refs counts subscribers plus callers still joining, so a watcher whose
	// joiners all give up before subscribing is stopped rather than leaked.
	refs   int
	subs   map[chan struct{}]struct{}
	closed bool
}

// subscribe returns an edge-triggered channel, buffered by one, that receives
// a value after relevant changes below root and is closed when ctx ends or the
// shared watcher stops.
func (h *hub) subscribe(ctx context.Context, root string) (<-chan struct{}, error) {
	for {
		h.mu.Lock()
		w := h.roots[root]
		creator := w == nil
		if creator {
			w = &rootWatch{root: root, ready: make(chan struct{}), subs: map[chan struct{}]struct{}{}}
			h.roots[root] = w
		}
		// Taken under h.mu: a watcher in the registry is never closed, so
		// this reference keeps it alive until it is released.
		w.mu.Lock()
		w.refs++
		w.mu.Unlock()
		h.mu.Unlock()
		if creator {
			h.start(w)
		}
		select {
		case <-w.ready:
		case <-ctx.Done():
			h.release(w)
			return nil, ctx.Err()
		}
		if w.err != nil {
			return nil, w.err
		}
		if err := ctx.Err(); err != nil {
			h.release(w)
			return nil, err
		}
		sub := make(chan struct{}, 1)
		w.mu.Lock()
		if w.closed {
			// The backend stopped after this lookup; join a fresh watcher.
			w.mu.Unlock()
			continue
		}
		w.subs[sub] = struct{}{}
		w.mu.Unlock()
		go func() {
			<-ctx.Done()
			h.unsubscribe(w, sub)
		}()
		return sub, nil
	}
}

// start runs the backend for w. The backend outlives any one subscriber's
// context, so it gets its own, cancelled when the last reference goes.
func (h *hub) start(w *rootWatch) {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	source, err := startRootWatch(ctx, w.root)
	if err != nil {
		w.err = err
		h.stop(w)
		close(w.ready)
		return
	}
	close(w.ready)
	go h.fanOut(w, source)
}

func (h *hub) fanOut(w *rootWatch, source <-chan struct{}) {
	for range source {
		w.mu.Lock()
		for sub := range w.subs {
			select {
			case sub <- struct{}{}:
			default:
			}
		}
		w.mu.Unlock()
	}
	// The backend stopped: either the last subscriber left, or it failed.
	// Close every remaining view so its stream ends instead of going quiet.
	h.stop(w)
	w.mu.Lock()
	for sub := range w.subs {
		delete(w.subs, sub)
		close(sub)
	}
	w.mu.Unlock()
}

func (h *hub) unsubscribe(w *rootWatch, sub chan struct{}) {
	w.mu.Lock()
	_, ok := w.subs[sub]
	if ok {
		delete(w.subs, sub)
		close(sub)
	}
	w.mu.Unlock()
	if ok {
		h.release(w)
	}
}

// release drops one reference and stops the watcher with the last one.
func (h *hub) release(w *rootWatch) {
	w.mu.Lock()
	w.refs--
	last := w.refs == 0
	w.mu.Unlock()
	if last {
		h.stop(w)
	}
}

// stop closes w to new subscribers, drops it from the registry and cancels
// its backend. Safe to call more than once.
func (h *hub) stop(w *rootWatch) {
	h.mu.Lock()
	if h.roots[w.root] == w {
		delete(h.roots, w.root)
	}
	h.mu.Unlock()
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.cancel()
}

// watching reports how many roots have a live shared watcher.
func (h *hub) watching() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.roots)
}
