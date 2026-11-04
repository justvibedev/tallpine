package migratevo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
)

// PSProcesses lists "pid command" lines via ps.
func PSProcesses(ctx context.Context) ([]string, error) {
	out, err := ExecRunner(ctx, "", "ps", "-axo", "pid=,command=")
	if err != nil {
		return nil, err
	}
	return strings.Split(string(out), "\n"), nil
}

// TmuxAlive reports whether `tmux -L vo` has a live server.
func TmuxAlive(ctx context.Context) bool {
	_, err := ExecRunner(ctx, "", "tmux", "-L", brand.LegacyTmuxSocketName, "list-sessions")
	return err == nil
}

// liveProcesses returns every process line that belongs to a running vo
// daemon or app: a binary executing out of the source root (worktree dev
// servers excluded), the app bundle, or the vo CLI running a long-lived
// subcommand. selfPID is skipped.
func liveProcesses(lines []string, source string, selfPID int) []string {
	var found []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		pidStr, cmd, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if pid, err := strconv.Atoi(pidStr); err == nil && pid == selfPID {
			continue
		}
		cmd = strings.TrimSpace(cmd)
		fields := strings.Fields(cmd)
		if len(fields) == 0 {
			continue
		}
		base := fields[0][strings.LastIndex(fields[0], "/")+1:]
		switch {
		case strings.HasPrefix(fields[0], source+"/") && !strings.HasPrefix(fields[0], source+"/data/worktrees/"):
			// A binary run out of the install itself (not a dev server in a worktree).
			found = append(found, line)
		case base == brand.LegacyCLIName && len(fields) > 1 && (fields[1] == "daemon" || fields[1] == "start" || fields[1] == "pty-host" || fields[1] == "chat-host"):
			found = append(found, line)
		case strings.Contains(cmd, brand.LegacyProductName+".app/"):
			found = append(found, line)
		}
	}
	return found
}

// staleEnvProcesses returns process lines that run from (or on files under)
// the source root's worktrees. They are not blockers, but they keep the TMPDIR
// they were started with (<source>/data/tmp/<session>) and recreate it, with
// tool caches such as node-compile-cache, after the tree has moved.
func staleEnvProcesses(lines []string, source string, selfPID int) []string {
	var out []string
	for _, line := range lines {
		pidStr, cmd, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || !strings.Contains(cmd, source+"/data/worktrees/") {
			continue
		}
		if pid, err := strconv.Atoi(pidStr); err == nil && pid == selfPID {
			continue
		}
		out = append(out, strings.TrimSpace(line))
	}
	return out
}

// Orphan is a leftover process (an orphaned Go test binary, say) whose
// executable lives under <source>/data/tmp/.
type Orphan struct {
	PID     int
	Command string
}

// orphanProcesses picks, from ps lines, only the processes whose executable is
// strictly under <source>/data/tmp/. The app, the daemon and every other
// process are never classified as orphans. selfPID is skipped.
func orphanProcesses(lines []string, source string, selfPID int) []Orphan {
	prefix := filepath.Join(source, "data", "tmp") + string(filepath.Separator)
	var out []Orphan
	for _, line := range lines {
		pidStr, cmd, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid <= 1 || pid == selfPID {
			continue
		}
		cmd = strings.TrimSpace(cmd)
		fields := strings.Fields(cmd)
		if len(fields) == 0 || !filepath.IsAbs(fields[0]) || filepath.Clean(fields[0]) != fields[0] {
			continue
		}
		if strings.HasPrefix(fields[0], prefix) {
			out = append(out, Orphan{PID: pid, Command: cmd})
		}
	}
	return out
}

// blockedByProcessesError lists every blocking process with its pid and, for
// orphans, the exact kill command.
func blockedByProcessesError(blocking []string, orphans []Orphan) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s process(es) are still running out of the old install:\n", len(blocking), brand.LegacyCLIName)
	for _, l := range blocking {
		fmt.Fprintf(&b, "  %s\n", l)
	}
	if len(orphans) > 0 {
		pids := make([]string, len(orphans))
		for i, o := range orphans {
			pids[i] = strconv.Itoa(o.PID)
		}
		fmt.Fprintf(&b, "%d of them are orphans under data/tmp; stop them with:\n  kill %s\nor rerun with --stop-orphans (SIGTERM to those only).\n", len(orphans), strings.Join(pids, " "))
	}
	b.WriteString("Quit the app and daemon first for anything else.")
	return errors.New(b.String())
}

// stopOrphans sends SIGTERM to the orphans and waits (up to settle) for them to exit.
func (e *Env) stopOrphans(orphans []Orphan) {
	kill := e.Kill
	if kill == nil {
		kill = func(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
	}
	for _, o := range orphans {
		if err := kill(o.PID); err == nil {
			_, _ = fmt.Fprintf(e.Out, "sent SIGTERM to orphan %d: %s\n", o.PID, o.Command)
		}
	}
	time.Sleep(e.OrphanSettle)
}

// liveNewProcess returns the first process line that belongs to a running
// new-brand app or daemon (the app bundle, or the CLI running a long-lived
// subcommand). selfPID and the migrator itself are skipped.
func liveNewProcess(lines []string, selfPID int) string {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		pidStr, cmd, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if pid, err := strconv.Atoi(pidStr); err == nil && pid == selfPID {
			continue
		}
		cmd = strings.TrimSpace(cmd)
		fields := strings.Fields(cmd)
		if len(fields) == 0 || strings.Contains(cmd, "migrate-from-vo") {
			continue
		}
		base := fields[0][strings.LastIndex(fields[0], "/")+1:]
		switch {
		case (base == brand.CLIName || base == brand.CLIAlias) && len(fields) > 1 &&
			(fields[1] == "daemon" || fields[1] == "start" || fields[1] == "pty-host" || fields[1] == "chat-host"):
			return line
		case strings.Contains(cmd, brand.ProductName+".app/Contents/"):
			return line
		}
	}
	return ""
}

// liveRunFilePID returns the pid recorded in the target's running.json when
// that process is alive and is not this one, else 0.
func (e *Env) liveRunFilePID() int {
	data, err := os.ReadFile(filepath.Join(e.Target, "running.json"))
	if err != nil {
		return 0
	}
	var rec struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal(data, &rec) != nil || rec.PID <= 0 || rec.PID == e.SelfPID {
		return 0
	}
	if syscall.Kill(rec.PID, 0) != nil {
		return 0
	}
	return rec.PID
}

// sameVolume reports whether a and b are on one filesystem (rename(2) needs it).
func sameVolume(a, b string) (bool, error) {
	var sa, sb syscall.Stat_t
	if err := syscall.Stat(a, &sa); err != nil {
		return false, err
	}
	if err := syscall.Stat(b, &sb); err != nil {
		return false, err
	}
	return sa.Dev == sb.Dev, nil
}

// CheckPreconditions refuses to migrate while the old install is alive, when
// the target would be clobbered, or when the rename would cross volumes.
// resuming relaxes only the "target exists" rule.
func (e *Env) CheckPreconditions(ctx context.Context, resuming bool) error {
	// After StepRename the old root is gone (no stub is left). A resume then
	// skips only what needs the source tree; every liveness check still runs.
	sourceGone := false
	if _, err := os.Stat(e.Source); err != nil {
		if _, tErr := os.Stat(e.Target); resuming && errors.Is(err, os.ErrNotExist) && tErr == nil {
			sourceGone = true
		} else {
			return fmt.Errorf("source %s: %w", e.Source, err)
		}
	}
	if e.Processes != nil {
		lines, err := e.Processes(ctx)
		if err != nil {
			return fmt.Errorf("list processes: %w", err)
		}
		orphans := orphanProcesses(lines, e.Source, e.SelfPID)
		if e.StopOrphans && len(orphans) > 0 {
			e.stopOrphans(orphans)
			if lines, err = e.Processes(ctx); err != nil {
				return fmt.Errorf("list processes: %w", err)
			}
			orphans = orphanProcesses(lines, e.Source, e.SelfPID)
		}
		if blocking := liveProcesses(lines, e.Source, e.SelfPID); len(blocking) > 0 {
			return blockedByProcessesError(blocking, orphans)
		}
		if stale := staleEnvProcesses(lines, e.Source, e.SelfPID); len(stale) > 0 && e.Out != nil {
			_, _ = fmt.Fprintf(e.Out, "warning: %d process(es) started from worktrees under %s keep their old TMPDIR and will recreate %s/data/tmp after the move; stop them (dev servers, esbuild) before or right after migrating:\n", len(stale), e.Source, e.Source)
			for _, l := range stale {
				_, _ = fmt.Fprintf(e.Out, "  %s\n", l)
			}
		}
	}
	if e.Processes != nil {
		lines, _ := e.Processes(ctx) // an error was already returned above
		if p := liveNewProcess(lines, e.SelfPID); p != "" {
			return fmt.Errorf("a %s process is still running (%s); quit the app first", brand.ProductName, p)
		}
	}
	if pid := e.liveRunFilePID(); pid != 0 {
		return fmt.Errorf("%s is running (pid %d from %s); quit the app first", brand.ProductName, pid, filepath.Join(e.Target, "running.json"))
	}
	if e.AliveTmux != nil && e.AliveTmux(ctx) {
		return fmt.Errorf("tmux server `tmux -L %s` is still running; kill it first", brand.LegacyTmuxSocketName)
	}
	if _, err := os.Lstat(e.Target); err == nil && !resuming && e.targetHoldsDB() {
		return fmt.Errorf("target %s already exists and there is no migration plan to resume", e.Target)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if sourceGone {
		return nil
	}
	same, err := sameVolume(e.Source, e.Home)
	if err != nil {
		return fmt.Errorf("compare volumes: %w", err)
	}
	if !same {
		return fmt.Errorf("%s and %s are on different volumes; rename(2) cannot move the tree", e.Source, e.Home)
	}
	return nil
}

// targetHoldsDB reports whether the target already carries a data DB. A target
// without one is only what the app creates on launch (Electron userData, logs)
// and is set aside rather than treated as a conflict.
func (e *Env) targetHoldsDB() bool {
	_, err := os.Lstat(e.TargetDB())
	return err == nil
}

// SetAsidePreexistingTarget renames a DB-less target out of the way so the
// whole-tree rename can take its place. It returns the new path ("" if none).
func (e *Env) SetAsidePreexistingTarget() (string, error) {
	if _, err := os.Lstat(e.Target); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	if e.targetHoldsDB() {
		return "", fmt.Errorf("target %s holds a data DB; refusing to move it", e.Target)
	}
	aside := fmt.Sprintf("%s.preexisting-%d", e.Target, time.Now().Unix())
	if err := os.Rename(e.Target, aside); err != nil {
		return "", fmt.Errorf("set aside %s: %w", e.Target, err)
	}
	return aside, nil
}
