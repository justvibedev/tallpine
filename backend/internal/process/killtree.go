package process

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

// treeKillTimeout bounds the external tree-kill helper (taskkill on Windows).
const treeKillTimeout = 5 * time.Second

// KillTree kills cmd's process and its descendants: the process group on
// Unix, taskkill /T /F on Windows. On Windows the helper is bounded by a 5 s
// timeout; if it fails or times out the root process is killed directly.
func KillTree(cmd *exec.Cmd) error {
	return KillTreeContext(context.Background(), cmd, treeKillTimeout)
}

// KillTreeContext is KillTree with a parent context and helper timeout (the
// Windows helper stops at whichever ends first). Both are ignored on Unix,
// where the kill is a single signal.
func KillTreeContext(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) error {
	return killProcessTreeContext(ctx, cmd, timeout)
}

// killTreeWith runs the tree-kill helper under timeout and falls back to
// killing the root process when it fails or does not finish in time.
func killTreeWith(ctx context.Context, cmd *exec.Cmd, timeout time.Duration, run func(ctx context.Context, pid int) error) error {
	if cmd.Process == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := run(ctx, cmd.Process.Pid); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}

// killPIDWith runs the pid-based tree-kill helper under timeout. There is no
// fallback: the caller has no handle on the process, only its verified pid.
func killPIDWith(pid int, force bool, timeout time.Duration, run func(ctx context.Context, pid int, force bool) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return run(ctx, pid, force)
}

// taskkillArgs is the one taskkill invocation for a process tree.
func taskkillArgs(pid int, force bool) []string {
	args := []string{"/PID", strconv.Itoa(pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	return args
}
