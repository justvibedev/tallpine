//go:build !windows

package deepseekharness

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// combinedOutputGrouped runs cmd in its own process group and kills the whole
// group when ctx ends or the command returns, so node children that dsh spawns
// cannot outlive the test (B151). exec.CommandContext alone kills only the
// direct child.
func combinedOutputGrouped(ctx context.Context, name string, args, env []string) ([]byte, int, error) {
	cmd := exec.Command(name, args...) //nolint:gosec // test helper; argv from the caller
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	var buf syncBuf
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Start(); err != nil {
		return nil, 0, err
	}
	pid := cmd.Process.Pid
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}()
	err := cmd.Wait()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil // a grandchild kept the pipe open; the group kill below reaps it
	}
	close(done)
	_ = syscall.Kill(-pid, syscall.SIGKILL) // reap stragglers after a clean exit too
	return buf.Bytes(), pid, err
}
