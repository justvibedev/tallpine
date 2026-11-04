//go:build !windows

package cleanup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/pathx"
	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

// inUseTimeout bounds one in-use probe; lsof walks the whole tree.
const inUseTimeout = time.Minute

// pathInUse reports whether a process has its working directory or an open
// file inside path. An error means it could not tell, and the caller keeps
// the path.
func pathInUse(ctx context.Context, path string) (bool, error) {
	if runtime.GOOS == "linux" {
		return procPathInUse(path)
	}
	ctx, cancel := context.WithTimeout(ctx, inUseTimeout)
	defer cancel()
	// -w drops warnings; +D lists every process with anything open below path.
	cmd := tallpineprocess.CommandContext(ctx, "lsof", "-w", "-t", "+D", path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if strings.TrimSpace(stdout.String()) != "" {
		return true, nil
	}
	var exit *exec.ExitError
	// lsof exits 1 with no output when nothing is open below path.
	if err == nil || (errors.As(err, &exit) && exit.ExitCode() == 1 && strings.TrimSpace(stderr.String()) == "" && ctx.Err() == nil) {
		return false, nil
	}
	return false, fmt.Errorf("lsof %s: %w: %s", path, err, bytes.TrimSpace(stderr.Bytes()))
}

// procPathInUse scans /proc for a working directory or an open file below
// path. Processes it may not inspect are other users', which cannot reach
// into a directory only the daemon's user owns.
func procPathInUse(path string) (bool, error) {
	forms := inUseForms(path)
	pids, err := filepath.Glob("/proc/[0-9]*")
	if err != nil {
		return false, fmt.Errorf("read /proc: %w", err)
	}
	if len(pids) == 0 {
		return false, errors.New("read /proc: no processes listed")
	}
	inside := func(link string) bool {
		target, err := os.Readlink(link)
		if err != nil {
			return false
		}
		for _, form := range forms {
			if pathx.Within(form, target) {
				return true
			}
		}
		return false
	}
	for _, pid := range pids {
		if inside(filepath.Join(pid, "cwd")) {
			return true, nil
		}
		fds, _ := os.ReadDir(filepath.Join(pid, "fd"))
		for _, fd := range fds {
			if inside(filepath.Join(pid, "fd", fd.Name())) {
				return true, nil
			}
		}
	}
	return false, nil
}

// inUseForms returns path and, when different, its symlink-resolved form:
// process tables report resolved paths.
func inUseForms(path string) []string {
	forms := []string{filepath.Clean(path)}
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != forms[0] {
		forms = append(forms, resolved)
	}
	return forms
}
