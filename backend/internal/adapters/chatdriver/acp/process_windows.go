//go:build windows

package acp

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"

	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP
	cmd.SysProcAttr.HideWindow = true
}

func killProcessTree(cmd *exec.Cmd) error {
	// Node launches the provider as a child; the shared tree kill takes the
	// whole tree and is bounded, so Close cannot hang on taskkill.
	return tallpineprocess.KillTree(cmd)
}
