package tmux

import (
	"context"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// run wraps runner.Run with a per-call timeout context.
func (r *Runtime) run(ctx context.Context, args ...string) ([]byte, error) {
	return r.runOnSocket(ctx, r.socketName, args...)
}

func (r *Runtime) runOnSocket(ctx context.Context, socketName string, args ...string) ([]byte, error) {
	if socketName != "" {
		args = append([]string{"-L", socketName}, args...)
	} else if r.socketName != "" {
		// Pin legacy discovery and commands to the historical machine default.
		// Without -L, tmux honors inherited TMUX and may target an unrelated
		// nested server whose session name happens to collide with Tallpine's handle.
		args = append([]string{"-L", "default"}, args...)
	}
	return r.runCommand(ctx, r.binaryForSocket(socketName), args...)
}

func (r *Runtime) binaryForSocket(socketName string) string {
	if socketName == "" && r.socketName != "" {
		return r.legacyBinary
	}
	return r.binary
}

// runForSession routes sessions created before Tallpine introduced its private tmux
// socket back to tmux's legacy default socket. The decision is discovered once
// per daemon lifetime and cached. New sessions always use socketName.
func (r *Runtime) runForSession(ctx context.Context, id string, args ...string) ([]byte, error) {
	socketName, err := r.socketForSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return r.runOnSocket(ctx, socketName, args...)
}

func (r *Runtime) socketForSession(ctx context.Context, id string) (string, error) {
	if r.socketName == "" {
		return "", nil
	}
	r.socketMu.RLock()
	socketName, ok := r.sessionSockets[id]
	r.socketMu.RUnlock()
	if ok {
		return socketName, nil
	}

	out, err := r.runOnSocket(ctx, r.socketName, hasSessionArgs(id)...)
	if err == nil {
		r.rememberSessionSocket(id, r.socketName)
		return r.socketName, nil
	}
	// Only cross the migration boundary when the private server definitively
	// lacks this session. An ambiguous probe stays on the private socket so a
	// transient error cannot redirect a live session elsewhere.
	if !sessionMissingOutput(string(out)) &&
		!serverNotRunningOutput(string(out)) &&
		!serverSocketAbsentOutput(string(out)) {
		return r.socketName, nil
	}
	if r.legacyBinary == "" {
		return "", fmt.Errorf(
			"%w: cannot inspect legacy system-socket session %s because system tmux is unavailable",
			ports.ErrRuntimeProbeInconclusive,
			id,
		)
	}
	legacyOut, legacyErr := r.runOnSocket(ctx, "", hasSessionArgs(id)...)
	if legacyErr == nil {
		// This session predates Tallpine's private socket, so it never ran through
		// Create and never got detach-on-destroy set (see setDetachOnDestroyOnArgs).
		// Destroy's own pre-kill reassertion is too late on its own: Tallpine can
		// Attach to this adopted session immediately, and the session can then
		// be destroyed by something Destroy never sees — the user exiting the
		// retained shell, or an external `kill-session` run directly against
		// the default socket. Enforce (and confirm) the guard right here, the
		// first moment Tallpine's daemon learns this session exists, so it is in
		// place before any caller (Attach included) can reach the session.
		// Fail closed and leave the session uncached on either socket so a
		// transient failure here stays retryable instead of caching an
		// unconfirmed adoption.
		if err := r.enforceDetachOnDestroy(ctx, "", id); err != nil {
			return "", err
		}
		r.rememberSessionSocket(id, "")
		return "", nil
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if sessionMissingOutput(string(legacyOut)) ||
		serverNotRunningOutput(string(legacyOut)) ||
		serverSocketAbsentOutput(string(legacyOut)) {
		// Both known sockets definitively lack the session. Return the private
		// target so IsAlive's ordinary exact-session handling resolves it: a
		// live private server reports the session missing, an absent one
		// reports ErrRuntimeUnavailable so recovery may recreate it.
		return r.socketName, nil
	}
	return "", fmt.Errorf(
		"%w: system tmux %q could not inspect legacy system-socket session %s: %w",
		ports.ErrRuntimeProbeInconclusive,
		r.legacyBinary,
		id,
		legacyErr,
	)
}

func (r *Runtime) rememberSessionSocket(id, socketName string) {
	r.socketMu.Lock()
	r.sessionSockets[id] = socketName
	r.socketMu.Unlock()
}

func (r *Runtime) forgetSessionSocket(id string) {
	r.socketMu.Lock()
	delete(r.sessionSockets, id)
	r.socketMu.Unlock()
}

func (r *Runtime) runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	out, err := r.runner.Run(cmdCtx, nil, name, args...)
	if cmdCtx.Err() != nil {
		return out, cmdCtx.Err()
	}
	if err != nil {
		return out, commandError{err: err, output: strings.TrimSpace(string(out))}
	}
	return out, nil
}

type commandError struct {
	err    error
	output string
}
