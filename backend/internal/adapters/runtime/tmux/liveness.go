package tmux

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// IsAlive reports whether the handle's session still exists via `tmux
// has-session`. Exit 0 means alive. A non-zero exit with output naming this
// session as missing is a definitive false, nil. A conclusively absent server
// — tmux ≥ 3.4 words it "error connecting … (No such file or directory)"
// rather than "no server running" — wraps ports.ErrRuntimeUnavailable so
// recovery may recreate it. A transient connection or protocol/client failure
// wraps ErrRuntimeProbeInconclusive so no caller can treat a possibly-live
// session as absent. Any other non-zero exit is a plain probe error, which is
// likewise never per-session death.
func (r *Runtime) IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	id, err := handleID(handle)
	if err != nil {
		return false, err
	}
	out, err := r.runForSession(ctx, id, hasSessionArgs(id)...)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if sessionMissingOutput(string(out)) {
				return false, nil
			}
			if serverNotRunningOutput(string(out)) || serverSocketAbsentOutput(string(out)) {
				return false, fmt.Errorf("tmux runtime: probe session %s: %w: %s",
					id, ports.ErrRuntimeUnavailable, strings.TrimSpace(string(out)))
			}
			if transientServerFailureOutput(string(out)) {
				return false, fmt.Errorf("tmux runtime: probe session %s: %w: %s",
					id, ports.ErrRuntimeProbeInconclusive, strings.TrimSpace(string(out)))
			}
		}
		return false, fmt.Errorf("tmux runtime: probe session %s: %w", id, err)
	}
	return true, nil
}

// ProcessRootPIDs returns every pane leader pid of the session so memory
// accounting can walk their descendants. A missing session yields no pids and
// no error; a tmux probe failure is surfaced so callers do not read it as zero.
func (r *Runtime) ProcessRootPIDs(ctx context.Context, handle ports.RuntimeHandle) ([]int, error) {
	id, err := handleID(handle)
	if err != nil {
		return nil, err
	}
	out, err := r.runForSession(ctx, id, listPanePIDsArgs(id)...)
	if err != nil {
		if sessionMissingOutput(string(out)) || serverNotRunningOutput(string(out)) || serverSocketAbsentOutput(string(out)) {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux runtime: list pane pids %s: %w", id, err)
	}
	var ids []int
	for _, line := range strings.Split(string(out), "\n") {
		pid, convErr := strconv.Atoi(strings.TrimSpace(line))
		if convErr != nil || pid <= 1 {
			continue
		}
		ids = append(ids, pid)
	}
	return ids, nil
}

// ServerPID returns the pid of Tallpine's own tmux server, which every session on
// this socket shares. It is not reachable by walking up from a pane: the
// server detaches on startup and is reparented to init, not to anything Tallpine
// already tracks. Zero, false when the server cannot be reached or the
// output cannot be parsed, and always without a private socket: the default
// server is the user's own tmux, and everything in it would read as Tallpine.
func (r *Runtime) ServerPID(ctx context.Context) (int, bool) {
	if r.socketName == "" {
		return 0, false
	}
	out, err := r.run(ctx, "display-message", "-p", "#{pid}")
	if err != nil {
		return 0, false
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(out)))
	if convErr != nil || pid <= 1 {
		return 0, false
	}
	return pid, true
}

// IsChildAlive also detects exited panes retained by tmux's remain-on-exit.
func (r *Runtime) IsChildAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	alive, err := r.IsAlive(ctx, handle)
	// Unlike agent-session recovery, shell reconciliation can forget a handle
	// when its server is conclusively absent (e.g. its last shell exited).
	// Transport and protocol failures remain inconclusive errors.
	if errors.Is(err, ports.ErrRuntimeUnavailable) {
		return false, nil
	}
	if err != nil || !alive {
		return false, err
	}
	out, err := r.runForSession(ctx, handle.ID, paneDeadArgs(handle.ID)...)
	if err != nil {
		return false, fmt.Errorf("tmux runtime: probe child status for %s: %w", handle.ID, err)
	}
	states := strings.Fields(string(out))
	if len(states) == 0 {
		return false, fmt.Errorf("tmux runtime: no pane status for %s: %w", handle.ID, ports.ErrRuntimeProbeInconclusive)
	}
	childAlive := false
	for _, state := range states {
		switch state {
		case "0":
			childAlive = true
		case "1":
		default:
			return false, fmt.Errorf("tmux runtime: invalid pane status %q for %s: %w", state, handle.ID, ports.ErrRuntimeProbeInconclusive)
		}
	}
	return childAlive, nil
}

// IsUnsupervisedReviewerAlive detects a live pre-supervisor reviewer process
// without mistaking the pane's preserved shell or the supervised exit sink for
// reviewer work.
func (r *Runtime) IsUnsupervisedReviewerAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	alive, err := r.IsAlive(ctx, handle)
	if err != nil || !alive {
		return false, err
	}
	entries, panePID, err := r.supervisedProcessTree(ctx, handle)
	if err != nil {
		return false, err
	}
	return containsUnsupervisedReviewerWorkload(entries, panePID), nil
}

// ProbeFencedRuntime returns liveness evidence for the exact fenced runtime identity.
func (r *Runtime) ProbeFencedRuntime(ctx context.Context, ref ports.FencedRuntimeRef) ports.FencedProbeResult {
	if ref.Handle.ID == "" || ref.SessionID == "" || strings.TrimSpace(ref.Generation) == "" || ref.Handle.ID != string(ref.SessionID) {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonIdentityMissing}
	}
	alive, err := r.IsAlive(ctx, ref.Handle)
	if err != nil {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
	}
	if !alive {
		return ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent}
	}
	entries, panePID, err := r.supervisedProcessTree(ctx, ref.Handle)
	if err != nil {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
	}
	descendants := descendantPIDs(entries, panePID)
	exactSupervisorFound := false
	for _, entry := range entries {
		if entry.pid == panePID || !descendants[entry.pid] || !isAnySupervisorCommand(entry.command) {
			continue
		}
		if isSupervisorCommand(entry.command, string(ref.SessionID), ref.Generation) {
			exactSupervisorFound = true
			continue
		}
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonGenerationMismatch}
	}
	if exactSupervisorFound {
		if containsExactSupervisedWorkload(entries, panePID, string(ref.SessionID), ref.Generation) {
			return ports.FencedProbeResult{Liveness: ports.FencedAlive, Reason: ports.FencedReasonExactMatch}
		}
		return ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent}
	}
	// A live pane without the exact Tallpine supervisor may contain a workload that a
	// user manually relaunched from the preserved shell. That is not proof of
	// the requested generation, but it is also not proof that the pane is dead.
	return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonIdentityMissing}
}

// IsSupervisedProcessAlive reports whether the managed workload for ref is
// still a descendant of this tmux pane. The initial launch is identified by
// its exact Tallpine supervisor. After that supervisor exits and leaves the
// interactive shell behind, a child launched from that shell is treated as a
// manually resumed workload. Command failures remain inconclusive.
func (r *Runtime) IsSupervisedProcessAlive(ctx context.Context, handle ports.RuntimeHandle, ref ports.SupervisedProcessRef) (bool, error) {
	entries, panePID, err := r.supervisedProcessTree(ctx, handle)
	if err != nil {
		return false, err
	}
	return containsManagedWorkload(entries, panePID, string(ref.SessionID), ref.LaunchID), nil
}

// IsExactSupervisedProcessAlive reports only the Tallpine supervisor matching ref
// while that supervisor still owns a live managed child. It deliberately
// excludes both the manual-child fallback used by the ordinary reaper probe
// and a supervisor that is merely waiting to durably report its child's exit:
// neither is proof that an agent can safely receive a continuation.
func (r *Runtime) IsExactSupervisedProcessAlive(ctx context.Context, handle ports.RuntimeHandle, ref ports.SupervisedProcessRef) (bool, error) {
	if ref.SessionID == "" || strings.TrimSpace(ref.LaunchID) == "" {
		return false, errors.New("tmux runtime: exact supervisor session and launch are required")
	}
	// A reviewer pane may be closed outside Tallpine. Confirm the handle first so a
	// definitively missing tmux session is reported as not alive instead of the
	// lower-level pane PID lookup surfacing it as an unexpected error.
	alive, err := r.IsAlive(ctx, handle)
	if err != nil || !alive {
		return false, err
	}
	entries, panePID, err := r.supervisedProcessTree(ctx, handle)
	if err != nil {
		return false, err
	}
	return containsExactSupervisedWorkload(entries, panePID, string(ref.SessionID), ref.LaunchID), nil
}

// HasSupervisedProcessRecord reports whether a Tallpine supervisor is present in
// the pane's process tree. A live pane without one may be a reviewer launched
// by an older Tallpine version, which must retain the legacy child-liveness probe.
func (r *Runtime) HasSupervisedProcessRecord(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	alive, err := r.IsAlive(ctx, handle)
	if err != nil || !alive {
		return false, err
	}
	entries, panePID, err := r.supervisedProcessTree(ctx, handle)
	if err != nil {
		return false, err
	}
	descendants := descendantPIDs(entries, panePID)
	for _, entry := range entries {
		if entry.pid != panePID && descendants[entry.pid] && isAnySupervisorCommand(entry.command) {
			return true, nil
		}
	}
	return false, nil
}

// RunningWorkloadCommands counts shell commands below the exact supervised
// agent for ref (see ports.WorkloadCommandInspector).
func (r *Runtime) RunningWorkloadCommands(ctx context.Context, handle ports.RuntimeHandle, ref ports.SupervisedProcessRef) (int, error) {
	if ref.SessionID == "" || strings.TrimSpace(ref.LaunchID) == "" {
		return 0, errors.New("tmux runtime: exact supervisor session and launch are required")
	}
	entries, panePID, err := r.supervisedProcessTree(ctx, handle)
	if err != nil {
		return 0, err
	}
	return countWorkloadCommands(entries, panePID, string(ref.SessionID), ref.LaunchID)
}

// countWorkloadCommands finds the agent the exact supervisor launched and
// counts the shells it spawned, with everything below them.
func countWorkloadCommands(entries []processEntry, panePID int, sessionID, launchID string) (int, error) {
	descendants := descendantPIDs(entries, panePID)
	supervisor := 0
	for _, entry := range entries {
		if entry.pid != panePID && descendants[entry.pid] && isSupervisorCommand(entry.command, sessionID, launchID) {
			supervisor = entry.pid
			break
		}
	}
	if supervisor == 0 {
		return 0, errors.New("tmux runtime: exact supervisor is not running")
	}
	agents := 0
	commands := 0
	for _, agent := range entries {
		if agent.ppid != supervisor {
			continue
		}
		agents++
		below := descendantPIDs(entries, agent.pid)
		for _, entry := range entries {
			if entry.pid == agent.pid || !below[entry.pid] || !isShellCommand(entry.command) {
				continue
			}
			// Count each shell once, with its subtree, from its outermost shell.
			if parent, ok := processByPID(entries, entry.ppid); ok && parent.pid != agent.pid && below[parent.pid] && isShellCommand(parent.command) {
				continue
			}
			commands += len(descendantPIDs(entries, entry.pid))
		}
	}
	if agents == 0 {
		return 0, errors.New("tmux runtime: supervised agent is not running")
	}
	return commands, nil
}

func processByPID(entries []processEntry, pid int) (processEntry, bool) {
	for _, entry := range entries {
		if entry.pid == pid {
			return entry, true
		}
	}
	return processEntry{}, false
}

// isShellCommand reports a POSIX or common interactive shell, the way agents
// run tool commands.
func isShellCommand(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	name := strings.TrimPrefix(filepath.Base(fields[0]), "-")
	switch name {
	case "sh", "bash", "zsh", "dash", "fish", "ksh":
		return true
	}
	return false
}

func (r *Runtime) supervisedProcessTree(ctx context.Context, handle ports.RuntimeHandle) ([]processEntry, int, error) {
	id, err := handleID(handle)
	if err != nil {
		return nil, 0, err
	}
	paneOut, err := r.runForSession(ctx, id, panePIDArgs(id)...)
	if err != nil {
		return nil, 0, fmt.Errorf("tmux runtime: inspect pane pid %s: %w", id, err)
	}
	panePID, err := strconv.Atoi(strings.TrimSpace(string(paneOut)))
	if err != nil || panePID <= 0 {
		return nil, 0, fmt.Errorf("tmux runtime: invalid pane pid %q", strings.TrimSpace(string(paneOut)))
	}
	processOut, err := r.runCommand(ctx, "ps", "-ww", "-axo", "pid=,ppid=,args=")
	if err != nil {
		return nil, 0, fmt.Errorf("tmux runtime: inspect process tree %s: %w", id, err)
	}
	entries, err := parseProcessTable(string(processOut))
	if err != nil {
		return nil, 0, fmt.Errorf("tmux runtime: parse process tree %s: %w", id, err)
	}
	return entries, panePID, nil
}
