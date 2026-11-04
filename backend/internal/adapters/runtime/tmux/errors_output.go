package tmux

import (
	"errors"
	"os/exec"
	"strings"
)

// sessionMissingOutput reports whether a non-zero `tmux has-session` exit is
// definitively "this session does not exist" — evidence about the probed
// session itself. Server-level failures deliberately do not match: "no server
// running" describes the whole server and "error connecting" is a transient
// socket failure; neither says anything about one session, so treating them as
// per-session death let a single server outage archive every session on the
// board (issue #3475).
//
// The wording depends on the command: has-session, kill-session and
// list-panes report "can't find session: <id>", while set-option and
// show-options against an exact "=<id>:" target report "no such session:
// =<id>:" (tmux 3.6a). Missing the latter made Destroy's pre-kill
// detach-on-destroy reassertion fail closed on an already-gone session, which
// broke every first reviewer launch on a fresh handle.
func sessionMissingOutput(out string) bool {
	s := strings.ToLower(out)
	return strings.Contains(s, "can't find session") ||
		strings.Contains(s, "no such session") ||
		strings.Contains(s, "session not found")
}

// duplicateSessionOutput identifies tmux's refusal to create a session whose
// name is already taken.
func duplicateSessionOutput(out string) bool {
	return strings.Contains(strings.ToLower(out), "duplicate session")
}

func serverNotRunningOutput(out string) bool {
	s := strings.ToLower(out)
	return strings.Contains(s, "no server running")
}

// serverSocketAbsentOutput identifies tmux output meaning the target's Unix
// socket file does not exist, so no server is listening there. tmux ≥ 3.4
// reports an absent server this way instead of "no server running". Like
// "no server running" this is definitive at the server level only; it must
// not become per-session evidence of death, because liveness callers still
// distinguish it from a conclusive "can't find session" and recovery paths
// may recreate the missing server.
func serverSocketAbsentOutput(out string) bool {
	s := strings.ToLower(out)
	return strings.Contains(s, "error connecting") &&
		strings.Contains(s, "no such file or directory")
}

func transientServerFailureOutput(out string) bool {
	s := strings.ToLower(out)
	return strings.Contains(s, "error connecting") ||
		strings.Contains(s, "protocol version mismatch") ||
		strings.Contains(s, "server exited unexpectedly")
}

// confirmedAbsentOutput reports whether a non-zero tmux exit definitively
// means the session or its server is gone, as opposed to a merely transient
// failure (transientServerFailureOutput's "connection refused" / protocol-
// mismatch / unexpected-exit cases) that leaves the session's actual state
// unknown. Every caller that decides whether it is safe to treat a tmux
// failure as "already gone" — Destroy's own kill-session result, its pre-kill
// detach-on-destroy reassertion, and enforceDetachOnDestroy's legacy-adoption
// enforcement — uses this and nothing broader: a transient failure proves
// nothing ran, so treating it as success let a caller believe teardown (or
// the detach-on-destroy guard) succeeded when it might not have, which could
// mean deleting a still-running session's worktree, or reopening the
// terminal/input transfer this guard exists to prevent (issue #4223).
func confirmedAbsentOutput(err error, out string) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) &&
		(sessionMissingOutput(out) || serverNotRunningOutput(out) || serverSocketAbsentOutput(out))
}
