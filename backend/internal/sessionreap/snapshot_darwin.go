package sessionreap

import (
	"bytes"
	"encoding/binary"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// szomb is the kinfo p_stat of a zombie, which no signal can stop.
const szomb = 5

// Snapshot lists the caller's own processes (same real uid) with their
// parent, start time, executable and session marker. kern.procargs2 is how
// ps(1) reads another process's environment.
func Snapshot() ([]Proc, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	uid := uint32(os.Getuid()) //nolint:gosec // uids fit in uint32.
	out := make([]Proc, 0, len(kps))
	for i := range kps {
		kp := &kps[i]
		if kp.Eproc.Pcred.P_ruid != uid || kp.Proc.P_stat == szomb {
			continue
		}
		p := Proc{
			PID:   int(kp.Proc.P_pid),
			PPID:  int(kp.Eproc.Ppid),
			Start: time.Unix(kp.Proc.P_starttime.Sec, int64(kp.Proc.P_starttime.Usec)*1000),
		}
		if raw, err := unix.SysctlRaw("kern.procargs2", p.PID); err == nil {
			p.Exe, p.Marker, p.UserShell = parseProcArgs(raw)
		}
		out = append(out, p)
	}
	return out, nil
}

// parseProcArgs reads a kern.procargs2 buffer: argc (int32), the exec path,
// NUL padding, argc arguments, then the environment, each NUL-terminated.
func parseProcArgs(raw []byte) (exe, marker string, userShell bool) {
	if len(raw) < 4 {
		return "", "", false
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return "", "", false
	}
	exe = string(rest[:end])
	rest = rest[end:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	fields := bytes.Split(rest, []byte{0})
	if argc > len(fields) {
		return exe, "", false
	}
	marker, userShell = envFacts(fields[argc:])
	return exe, marker, userShell
}
