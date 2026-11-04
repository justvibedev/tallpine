//go:build windows

package process

import (
	"context"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP
	cmd.SysProcAttr.HideWindow = true
}

func killProcessTreeContext(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) error {
	return killTreeWith(ctx, cmd, timeout, func(ctx context.Context, pid int) error { return runTaskkill(ctx, pid, true) })
}

func killProcessTree(cmd *exec.Cmd) error { return KillTree(cmd) }

// KillPIDTree takes pid's whole tree down with taskkill /T (/F when force),
// bounded by the shared timeout. Callers must have verified pid's identity.
func KillPIDTree(pid int, force bool) error {
	return killPIDWith(pid, force, treeKillTimeout, runTaskkill)
}

// runTaskkill is the Windows equivalent of killing the Unix process group: it
// also takes down grandchildren (Node launches providers as children).
func runTaskkill(ctx context.Context, pid int, force bool) error {
	kill := exec.CommandContext(ctx, "taskkill", taskkillArgs(pid, force)...)
	kill.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	return kill.Run()
}
