package sessionreap

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// clockTicks is USER_HZ, 100 on every Linux the daemon targets.
const clockTicks = 100

// Snapshot lists the processes whose /proc entries the caller can read: its
// own, since another user's environ is unreadable.
func Snapshot() ([]Proc, error) {
	dirs, err := filepath.Glob("/proc/[0-9]*")
	if err != nil {
		return nil, err
	}
	boot := bootTime()
	uid := os.Getuid()
	out := make([]Proc, 0, len(dirs))
	for _, dir := range dirs {
		pid, err := strconv.Atoi(filepath.Base(dir))
		if err != nil {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !ownedBy(info, uid) {
			continue
		}
		stat, err := os.ReadFile(filepath.Join(dir, "stat"))
		if err != nil {
			continue
		}
		// Fields after the parenthesised command: state ppid ... starttime(22).
		i := bytes.LastIndexByte(stat, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(string(stat[i+1:]))
		if len(f) < 20 || f[0] == "Z" {
			continue
		}
		p := Proc{PID: pid}
		p.PPID, _ = strconv.Atoi(f[1])
		if ticks, err := strconv.ParseInt(f[19], 10, 64); err == nil && !boot.IsZero() {
			p.Start = boot.Add(time.Duration(ticks) * time.Second / clockTicks)
		}
		p.Exe, _ = os.Readlink(filepath.Join(dir, "exe"))
		if env, err := os.ReadFile(filepath.Join(dir, "environ")); err == nil {
			p.Marker, p.UserShell = envFacts(bytes.Split(env, []byte{0}))
		}
		out = append(out, p)
	}
	return out, nil
}

func bootTime() time.Time {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			if sec, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				return time.Unix(sec, 0)
			}
		}
	}
	return time.Time{}
}
