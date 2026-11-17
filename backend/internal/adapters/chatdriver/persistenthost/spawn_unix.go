//go:build !windows

package persistenthost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func spawnDetached(ctx context.Context, cfg Config) (<-chan struct{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, hostArgs(cfg)...) //nolint:forbidigo // detached chat host has specialized lifetime and session ownership
	cmd.Dir = cfg.Workdir
	cmd.Env = cfg.Env
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn detached chat host: %w", err)
	}
	// Setsid detaches the host from our session, but it remains our child until
	// Wait reaps it. Release alone leaves a zombie after each hibernation.
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	return exited, nil
}
