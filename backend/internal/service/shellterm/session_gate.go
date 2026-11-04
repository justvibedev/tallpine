package shellterm

import (
	"context"
	"fmt"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// sessionGate is the admission/teardown barrier for one session's scoped
// shell terminals. It is held for the ENTIRE span from BeginSessionTeardown
// through the release function it returns on success — deliberately crossing
// the call into Session Manager's own worktree teardown in between — so an
// OpenShellTerminal racing against that window waits until the window ends,
// rather than slipping a new shell in against a worktree that is mid-removal.
// By the time a waiter acquires the gate, the teardown it waited on has fully
// finished (destroyed or preserved), so there is nothing left to check here:
// resolveShellTerminalWorkingDir's own existence check is what decides whether
// that Open lands in the worktree or falls back.
//
// The gate is a capacity-1 channel rather than a sync.Mutex because
// sync.Mutex.Lock is uninterruptible: a teardown whose workspace.Destroy
// stalls (hung git subprocess, a Windows directory handle that never releases)
// would park every waiter forever, and an HTTP handler blocked in Lock cannot
// observe its client disconnecting or its context deadline. A channel send is
// selectable, so acquire can honour both.
type sessionGate struct {
	ch chan struct{}
}

func newSessionGate() *sessionGate {
	return &sessionGate{ch: make(chan struct{}, 1)}
}

// sessionGateWaitTimeout bounds how long a contended acquire waits before
// giving up. A teardown holding the gate is doing real filesystem work
// (worktree removal), so the budget is generous — but finite, so a wedged
// teardown surfaces as a retryable 409 instead of a request that never
// returns. A var so tests need not wait it out.
var sessionGateWaitTimeout = 30 * time.Second

// acquire takes the gate, returning a release function tied to THIS
// acquisition (calling it more than once is a no-op, and it can never release
// a different acquisition's hold). It gives up when ctx is done or the wait
// budget expires, so no caller parks indefinitely behind a stalled teardown.
func (g *sessionGate) acquire(ctx context.Context, id domain.SessionID) (release func(), err error) {
	var once sync.Once
	releaseFn := func() { once.Do(func() { <-g.ch }) }

	// Fast path: uncontended, no timer allocation.
	select {
	case g.ch <- struct{}{}:
		return releaseFn, nil
	default:
	}

	timer := time.NewTimer(sessionGateWaitTimeout)
	defer timer.Stop()
	select {
	case g.ch <- struct{}{}:
		return releaseFn, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("shell terminal gate for session %s: %w", id, ctx.Err())
	case <-timer.C:
		return nil, apierr.Conflict("SHELL_TERMINAL_SESSION_BUSY",
			"This session's shell terminals are being torn down; try again in a moment", nil)
	}
}
