//go:build !windows

package testenv

import (
	"os/exec"
	"path/filepath"
)

// killTmuxServers runs kill-server against every socket under dir
// (<dir>/tmux-<uid>/<name>), so servers started with TMUX_TMPDIR=dir die
// before the dir is removed. Best effort.
func killTmuxServers(dir string) {
	socks, _ := filepath.Glob(filepath.Join(dir, "tmux-*", "*"))
	for _, s := range socks {
		_ = exec.Command("tmux", "-S", s, "kill-server").Run() //nolint:forbidigo // test-only helper; targets only this sandbox's sockets.
	}
}

// killTmuxSocket kills the server on the named -L socket. Best effort.
func killTmuxSocket(name string) {
	_ = exec.Command("tmux", "-L", name, "kill-server").Run() //nolint:forbidigo // test-only helper; targets only the test's own socket.
}
