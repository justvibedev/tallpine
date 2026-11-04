package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

// killSessionsByPID force-terminates every process in each pid's tmux pane
// session. tmux runs each pane in its own session (pane pid == session id), so
// signaling the session reaps the pane's background children — e.g. a dev
// server a worker started with `&` — that `kill-session`'s SIGHUP leaves
// running. It SIGTERMs, waits grace for a clean exit, then
// SIGKILLs survivors. Best-effort: `pkill` is absent on Windows, where tmux is
// never the runtime, so the calls simply no-op there.
func killSessionsByPID(ctx context.Context, pids []int, grace time.Duration) {
	reapPaneSessions(ctx, pids, grace, signalSessions, sessionsHaveProcesses)
}

// reapPaneSessions is killSessionsByPID's logic with the pkill/pgrep calls
// injected, so the SIGTERM → wait → SIGKILL sequence is testable without real
// processes.
func reapPaneSessions(
	ctx context.Context,
	pids []int,
	grace time.Duration,
	signal func(ctx context.Context, pids []int, sig string) bool,
	hasProcesses func(ctx context.Context, pids []int) bool,
) {
	if len(pids) == 0 {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), grace+5*time.Second)
	defer cancel()

	// `-s` is a Linux procps extension; BSD/macOS pkill rejects it outright. When
	// the platform cannot signal by session id, no amount of waiting reaps
	// anything — the SIGTERM never landed and the SIGKILL would not either — so
	// return instead of blocking the caller for the whole grace. Destroy runs
	// inside the shell-terminal DELETE handler, and that dead wait was the
	// several-second delay users saw when closing a terminal on macOS.
	if !signal(cleanupCtx, pids, "-TERM") {
		return
	}
	if !hasProcesses(cleanupCtx, pids) {
		return
	}

	// Poll rather than sleep the whole grace. Callers block on this (Destroy runs
	// inside the shell-terminal DELETE handler), and the common case — an
	// interactive shell with nothing behind it — is empty almost immediately. A
	// process that really needs the time still gets the full grace before SIGKILL.
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	ticker := time.NewTicker(reapPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-cleanupCtx.Done():
			return
		case <-ticker.C:
			if !hasProcesses(cleanupCtx, pids) {
				return
			}
		case <-deadline.C:
			if !hasProcesses(cleanupCtx, pids) {
				return
			}
			signal(cleanupCtx, pids, "-KILL")
			return
		}
	}
}

// signalSessions sends a pkill signal flag (e.g. "-TERM") to every process in
// each pane session, matched by session id via `pkill -s`. It reports whether
// the platform supports signalling by session id at all: exit 2 is a usage
// error on both procps and BSD pkill, which is how macOS answers `-s`, and
// there the call reaches no process.
func signalSessions(ctx context.Context, pids []int, sig string) bool {
	supported := false
	for _, pid := range pids {
		err := tallpineprocess.CommandContext(ctx, "pkill", sig, "-s", strconv.Itoa(pid)).Run()
		if !isUnsupportedMatcher(err) {
			supported = true
		}
	}
	return supported
}

// isUnsupportedMatcher reports whether a pgrep/pkill invocation failed because
// the platform rejects the matcher itself (exit 2, a usage error) rather than
// because nothing matched (exit 1) or the process is missing entirely.
func isUnsupportedMatcher(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode() >= 2
	}
	// pkill/pgrep absent (Windows, minimal containers): equally unusable.
	return true
}

// sessionsHaveProcesses reports whether any process remains in the pane
// sessions. `pgrep` exit 1 means no matches; other failures are treated as
// survivors so Destroy stays conservative and still attempts SIGKILL.
func sessionsHaveProcesses(ctx context.Context, pids []int) bool {
	for _, pid := range pids {
		err := tallpineprocess.CommandContext(ctx, "pgrep", "-s", strconv.Itoa(pid)).Run()
		if err == nil || ctx.Err() != nil {
			return true
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return true
		}
	}
	return false
}

// Create starts a new tmux session in the workspace, running the agent's
// launch command with a keep-alive shell, and returns a handle to it.
func (r *Runtime) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	id, err := tmuxSessionName(cfg.SessionID)
	if err != nil {
		return ports.RuntimeHandle{}, tmuxCreateFailure(err)
	}
	if cfg.WorkspacePath == "" {
		return ports.RuntimeHandle{}, tmuxCreateFailure(errors.New("tmux runtime: workspace path is required"))
	}
	if len(cfg.Argv) == 0 {
		return ports.RuntimeHandle{}, tmuxCreateFailure(errors.New("tmux runtime: launch command is required"))
	}
	if err := validateEnvKeys(cfg.Env); err != nil {
		return ports.RuntimeHandle{}, tmuxCreateFailure(err)
	}

	envFile, err := writeLaunchEnvFile(cfg)
	if err != nil {
		return ports.RuntimeHandle{}, tmuxCreateFailure(fmt.Errorf("tmux runtime: write launch env: %w", err))
	}
	launchCmd := buildLaunchCommandWithEnvFile(cfg, envFile)
	args := newSessionArgs(id, cfg.WorkspacePath, r.shell, launchCmd)
	if out, err := r.run(ctx, args...); err != nil {
		_ = os.Remove(envFile)
		createErr := fmt.Errorf("tmux runtime: create session %s: %w", id, err)
		if duplicateSessionOutput(string(out)) {
			// The name already belongs to a live session that this call did not
			// start; cleaning it up by name would kill someone else's session.
			return ports.RuntimeHandle{}, tmuxCreateFailure(createErr)
		}
		// A timed-out or otherwise unconfirmed new-session may still complete
		// server-side. Pin any cleanup to Tallpine's own socket so Destroy never probes
		// or adopts a same-named session on tmux's default socket.
		r.rememberSessionSocket(id, r.socketName)
		return ports.RuntimeHandle{}, tmuxPossibleCreateFailure(
			createErr,
			ports.RuntimeHandle{ID: id},
			ports.RuntimeCleanupNotAttempted,
		)
	}
	r.rememberSessionSocket(id, r.socketName)
	handle := ports.RuntimeHandle{ID: id}
	if err := r.verifyPaneWorkingDirectory(ctx, id, cfg.WorkspacePath); err != nil {
		return ports.RuntimeHandle{}, r.failedCreatedRuntime(handle, err)
	}

	// Hide the status bar in the embedded terminal: it clutters the view and
	// was not designed for the in-browser display context.
	if _, err := r.run(ctx, setStatusOffArgs(id)...); err != nil {
		return ports.RuntimeHandle{}, r.failedCreatedRuntime(handle, fmt.Errorf("tmux runtime: set status %s: %w", id, err))
	}

	// Enable mouse mode so the embedded terminal's SGR wheel reports scroll the
	// pane (see setMouseOnArgs). Without it, wheel scrolling silently no-ops.
	if _, err := r.run(ctx, setMouseOnArgs(id)...); err != nil {
		return ports.RuntimeHandle{}, r.failedCreatedRuntime(handle, fmt.Errorf("tmux runtime: set mouse %s: %w", id, err))
	}

	// Size the shared window to the largest attached client, not the most recent
	// one, so a small secondary viewer (e.g. the phone) can't strip down a larger
	// client's view (see setWindowSizeLargestArgs).
	if _, err := r.run(ctx, setWindowSizeLargestArgs(id)...); err != nil {
		return ports.RuntimeHandle{}, r.failedCreatedRuntime(handle, fmt.Errorf("tmux runtime: set window-size %s: %w", id, err))
	}

	// Make the attach client exit when this session is destroyed rather than
	// hop onto one of the user's own sessions under a `detach-on-destroy off`
	// tmux.conf (see setDetachOnDestroyOnArgs).
	if _, err := r.run(ctx, setDetachOnDestroyOnArgs(id)...); err != nil {
		return ports.RuntimeHandle{}, r.failedCreatedRuntime(handle, fmt.Errorf("tmux runtime: set detach-on-destroy %s: %w", id, err))
	}

	alive, err := r.IsAlive(ctx, handle)
	if err != nil {
		return ports.RuntimeHandle{}, r.failedCreatedRuntime(handle, fmt.Errorf("tmux runtime: verify session %s: %w", id, err))
	}
	if !alive {
		return ports.RuntimeHandle{}, r.failedCreatedRuntime(handle, fmt.Errorf("tmux runtime: session %s exited before ready", id))
	}
	return handle, nil
}

func (r *Runtime) failedCreatedRuntime(handle ports.RuntimeHandle, cause error) error {
	if cleanupErr := r.Destroy(context.Background(), handle); cleanupErr != nil {
		return tmuxPossibleCreateFailure(errors.Join(cause, cleanupErr), handle, ports.RuntimeCleanupFailed)
	}
	return tmuxPossibleCreateFailure(cause, handle, ports.RuntimeCleanupSucceeded)
}

// Restart replaces the command in an existing pane while preserving the tmux
// session. This is used to resume an exited agent without discarding terminal
// history or forcing attached clients onto a new handle.
func (r *Runtime) Restart(ctx context.Context, handle ports.RuntimeHandle, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	id, err := handleID(handle)
	if err != nil {
		return ports.RuntimeHandle{}, err
	}
	expectedID, err := tmuxSessionName(cfg.SessionID)
	if err != nil {
		return ports.RuntimeHandle{}, err
	}
	if expectedID != id {
		return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: restart handle %s does not match session %s", id, cfg.SessionID)
	}
	if cfg.WorkspacePath == "" {
		return ports.RuntimeHandle{}, errors.New("tmux runtime: workspace path is required")
	}
	if len(cfg.Argv) == 0 {
		return ports.RuntimeHandle{}, errors.New("tmux runtime: launch command is required")
	}
	if err := validateEnvKeys(cfg.Env); err != nil {
		return ports.RuntimeHandle{}, err
	}

	envFile, err := writeLaunchEnvFile(cfg)
	if err != nil {
		return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: write launch env: %w", err)
	}
	launchCmd := buildLaunchCommandWithEnvFile(cfg, envFile)
	if _, err := r.runForSession(ctx, id, respawnPaneArgs(id, cfg.WorkspacePath, r.shell, launchCmd)...); err != nil {
		_ = os.Remove(envFile)
		return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: restart session %s: %w", id, err)
	}
	alive, err := r.IsAlive(ctx, handle)
	if err != nil {
		return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: verify restarted session %s: %w", id, err)
	}
	if !alive {
		return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: session %s exited during restart", id)
	}
	return handle, nil
}

// paneCwdVerifyAttempts and paneCwdVerifyRetryDelay bound how long Create
// waits for the pane's working directory to settle before giving up.
// buildLaunchCommand's `cd '<workspace>' || exit;` guard corrects a pane that
// started in the tmux server's own (possibly poisoned) cwd, but only once the
// pane's shell actually runs that cd. Measured live on 2026-07-25:
// #{pane_current_path} sampled immediately after `new-session` was stale, and
// the same probe sampled 50ms later was already correct. A single-shot check
// therefore lost that race every time and turned a spawn that was actually
// going to succeed into a hard failure (issue #2775): retrying gives the cd
// guard the moment it needs to run.
const (
	paneCwdVerifyAttempts   = 5
	paneCwdVerifyRetryDelay = 50 * time.Millisecond
)

func (r *Runtime) verifyPaneWorkingDirectory(ctx context.Context, id, want string) error {
	var lastErr error
	for attempt := 0; attempt < paneCwdVerifyAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(paneCwdVerifyRetryDelay):
			}
		}
		out, err := r.runForSession(ctx, id, paneCurrentPathArgs(id)...)
		if err != nil {
			// A later transient probe failure (e.g. a one-off tmux CLI hiccup)
			// must not overwrite an already-observed cwd mismatch: the mismatch
			// is the classifiable, actionable error toAPIError maps via
			// ports.ErrRuntimeWorkspaceCwdMismatch (Fix 4), and losing it here
			// would silently regress that mapping back to a bare, unclassifiable
			// 500 whenever the very last attempt happened to hit a probe error.
			if !errors.Is(lastErr, ports.ErrRuntimeWorkspaceCwdMismatch) {
				lastErr = fmt.Errorf("tmux runtime: verify working directory %s: %w", id, err)
			}
			continue
		}
		got := strings.TrimSpace(string(out))
		if sameDirectory(got, want) {
			return nil
		}
		lastErr = fmt.Errorf(
			"%w: session %s started in %q, want %q (the worktree may be missing, or the tmux server may be pinned to a stale directory)",
			ports.ErrRuntimeWorkspaceCwdMismatch, id, got, want,
		)
	}
	return lastErr
}

// Destroy kills the handle's tmux session and reaps the pane processes it
// leaves behind. `tmux kill-session` only SIGHUPs each pane's foreground
// process, so a worker's backgrounded children (e.g. a dev server started with
// `&`, later reparented to init) survive it and hold their ports indefinitely
// (issue #2523). To catch those, Destroy records each pane's session id before
// teardown and, after kill-session, signals the whole session (see
// killSessionsByPID). An already-gone session is treated as success (idempotent).
func (r *Runtime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	id, err := handleID(handle)
	if err != nil {
		return err
	}
	// Capture pane session ids while the session still exists; a missing
	// session lists no panes and reaps nothing. Best-effort: failures here must
	// not block the kill-session below.
	sessionIDs := r.paneSessionIDs(ctx, id)

	// Re-assert detach-on-destroy immediately before kill-session too. This is
	// belt-and-suspenders alongside enforceDetachOnDestroy (which now covers
	// the same guard at legacy-socket adoption time, before Attach can ever
	// reach the session — see socketForSession): Create also sets this for
	// every session it starts (see setDetachOnDestroyOnArgs), but re-asserting
	// here costs one extra call and catches the option ever having been
	// changed back in between. Fail closed on any other error: kill-session
	// must not run while the guard is unconfirmed, because a session that dies
	// with detach-on-destroy still off can hand Tallpine's terminal, and its input,
	// to one of the user's own sessions (issue #4223).
	if out, setErr := r.runForSession(ctx, id, setDetachOnDestroyOnArgs(id)...); setErr != nil && !confirmedAbsentOutput(setErr, string(out)) {
		return fmt.Errorf("tmux runtime: set detach-on-destroy %s: %w", id, setErr)
	}

	out, err := r.runForSession(ctx, id, killSessionArgs(id)...)
	// Reap regardless of the kill-session result: orphaned children outlive the
	// session, so they must be cleaned up even when the session was already
	// gone (a benign double-kill).
	r.reapSessions(ctx, sessionIDs, r.reapGrace)

	if err != nil {
		// Only confirmed session/server/socket absence is safe to read as
		// "already gone" (see confirmedAbsentOutput). A transient failure here
		// (connection refused, protocol mismatch, unexpected server exit) does
		// not prove kill-session actually ran: treating it as success used to
		// forget the socket mapping and return nil, so a caller could mark the
		// session terminated and delete its worktree while the session, and
		// its detach-on-destroy guard, might still be alive. Returning the
		// error instead, and leaving the socket mapping intact, keeps this
		// retryable: a caller that calls Destroy again finds the same cached
		// socket rather than re-running discovery from scratch.
		if confirmedAbsentOutput(err, string(out)) {
			r.forgetSessionSocket(id)
			return nil
		}
		return fmt.Errorf("tmux runtime: destroy session %s: %w", id, err)
	}
	r.forgetSessionSocket(id)
	return nil
}

// paneSessionIDs lists the pid of every pane in the session. tmux launches each
// pane in its own session (setsid), so a pane's pid is also its session id —
// the handle killSessionsByPID uses to reap the pane's descendants. Best-effort:
// any error (including a missing session) or unparseable line yields no ids,
// and pids <= 1 are skipped so we never signal init or the "current session".
func (r *Runtime) paneSessionIDs(ctx context.Context, id string) []int {
	out, err := r.runForSession(ctx, id, listPanePIDsArgs(id)...)
	if err != nil {
		return nil
	}
	var ids []int
	for _, line := range strings.Split(string(out), "\n") {
		pid, convErr := strconv.Atoi(strings.TrimSpace(line))
		if convErr != nil || pid <= 1 {
			continue
		}
		ids = append(ids, pid)
	}
	return ids
}

// enforceDetachOnDestroy sets and reads back the session-scoped
// detach-on-destroy option for id on socketName, so a session Tallpine did not
// create (currently only a legacy-socket adoption, see socketForSession) is
// guarded before anything — Attach included — can reach it. Setting alone is
// not trusted: it is verified with a second read so a target that silently
// resolved to the wrong session (see setDetachOnDestroyOnArgs) cannot pass
// unnoticed. Fails closed on any unconfirmed error; a definitively missing
// session/server is treated as nothing left to guard, matching Destroy's
// own handling, and is not cached, so a transient failure stays retryable.
func (r *Runtime) enforceDetachOnDestroy(ctx context.Context, socketName, id string) error {
	if out, err := r.runOnSocket(ctx, socketName, setDetachOnDestroyOnArgs(id)...); err != nil {
		if confirmedAbsentOutput(err, string(out)) {
			return nil
		}
		return fmt.Errorf("tmux runtime: set detach-on-destroy for adopted session %s: %w", id, err)
	}
	out, err := r.runOnSocket(ctx, socketName, showDetachOnDestroyArgs(id)...)
	if err != nil {
		if confirmedAbsentOutput(err, string(out)) {
			return nil
		}
		return fmt.Errorf("tmux runtime: verify detach-on-destroy for adopted session %s: %w", id, err)
	}
	if got := strings.TrimSpace(string(out)); got != "on" {
		return fmt.Errorf("tmux runtime: detach-on-destroy for adopted session %s reports %q, want \"on\"", id, got)
	}
	return nil
}

// -- text helpers --
